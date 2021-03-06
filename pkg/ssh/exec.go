// Copyright 2020 The Okteto Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ssh

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/okteto/okteto/pkg/log"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/terminal"
)

// Exec executes the command over SSH
func Exec(ctx context.Context, remotePort int, tty bool, inR io.Reader, outW, errW io.Writer, command []string) error {
	log.Info("starting SSH connection")
	sshConfig, err := getSSHClientConfig()
	if err != nil {
		return fmt.Errorf("failed to get SSH configuration: %s", err)
	}

	var connection *ssh.Client
	t := time.NewTicker(100 * time.Millisecond)
	for i := 0; i < 100; i++ {
		connection, err = ssh.Dial("tcp", fmt.Sprintf("localhost:%d", remotePort), sshConfig)
		if err == nil {
			break
		}

		log.Debugf("failed to connect to SSH server, will retry: %s", err)
		<-t.C
	}

	if err != nil {
		return fmt.Errorf("failed to connect to SSH server: %s", err)
	}

	defer connection.Close()

	session, err := connection.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session: %s", err)
	}

	defer session.Close()

	if tty {
		modes := ssh.TerminalModes{
			ssh.ECHO:  0, // Disable echoing
			ssh.IGNCR: 1, // Ignore CR on input
		}

		state, err := terminal.MakeRaw(0)
		if err != nil {
			return fmt.Errorf("request for raw terminal failed: %s", err)
		}

		defer func() {
			if err := terminal.Restore(0, state); err != nil {
				log.Infof("failed to restore terminal: %s", err)
			}
		}()

		width, height, err := terminal.GetSize(0)
		if err != nil {
			return fmt.Errorf("request for terminal size failed: %s", err)
		}

		if err := session.RequestPty("xterm", height, width, modes); err != nil {
			return fmt.Errorf("request for pseudo terminal failed: %s", err)
		}
	}

	sockEnvVar, ok := os.LookupEnv("SSH_AUTH_SOCK")
	if !ok {
		log.Info("SSH_AUTH_SOCK is not set, not forwarding socket")
	} else {
		if err := agent.ForwardToRemote(connection, sockEnvVar); err != nil {
			log.Infof("failed to existing SSH_AUTH_SOCK('%s'): %s", sockEnvVar, err)
		}
		if err := agent.RequestAgentForwarding(session); err != nil {
			log.Infof("failed to forward ssh agent to remote: %s", err)
		}
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("unable to setup stdin for session: %v", err)
	}
	go func() {
		if _, err = io.Copy(stdin, inR); err != nil {
			log.Infof("error while reading from stdIn: %s", err)
		}
	}()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return fmt.Errorf("unable to setup stdout for session: %v", err)
	}

	go func() {
		if _, err := io.Copy(outW, stdout); err != nil {
			log.Infof("error while writing to stdOut: %s", err)
		}
	}()

	stderr, err := session.StderrPipe()
	if err != nil {
		return fmt.Errorf("unable to setup stderr for session: %v", err)
	}

	go func() {
		if _, err := io.Copy(errW, stderr); err != nil {
			log.Infof("error while writing to stdOut: %s", err)
		}
	}()

	cmd := strings.Join(command, " ")
	log.Infof("executing command over SSH: '%s'", cmd)
	return session.Run(cmd)
}
