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

package okteto

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/machinebox/graphql"
	"github.com/okteto/okteto/pkg/config"
	"github.com/okteto/okteto/pkg/errors"
	"github.com/okteto/okteto/pkg/log"
)

const (
	tokenFile = ".token.json"
)

// Token contains the auth token and the URL it belongs to
type Token struct {
	Token     string `json:"Token"`
	URL       string `json:"URL"`
	ID        string `json:"ID"`
	MachineID string `json:"MachineID"`
	Buildkit  string `json:"Buildkit"`
	Registry  string `json:"Registry"`
}

// User contains the auth information of the logged in user
type User struct {
	Name        string
	Email       string
	GithubID    string
	Token       string
	ID          string
	New         bool
	Buildkit    string
	Registry    string
	Certificate string
}

type u struct {
	Auth User
}

type q struct {
	User User
}

var currentToken *Token

// AuthWithToken authenticates in okteto with the provided token
func AuthWithToken(ctx context.Context, url, token string) (*User, error) {
	client, err := getClient(url)
	if err != nil {
		return nil, err
	}

	user, err := queryUser(ctx, client, token)
	if err != nil {
		log.Infof("failed to login the user: %s", err)
		return nil, fmt.Errorf("invalid API token")
	}

	if err := saveAuthData(&user.User, url); err != nil {
		log.Infof("failed to save the login data: %s", err)
		return nil, fmt.Errorf("failed to save the login data locally")
	}

	return &user.User, nil
}

// Auth authenticates in okteto with a github OAuth code
func Auth(ctx context.Context, code, url string) (*User, error) {
	client, err := getClient(url)
	if err != nil {
		return nil, err
	}

	user, err := authUser(ctx, client, code)
	if err != nil {
		return nil, err
	}

	if err := saveAuthData(&user.Auth, url); err != nil {
		return nil, err
	}

	return &user.Auth, nil

}

func saveAuthData(user *User, url string) error {
	if user.GithubID == "" || user.Token == "" {
		return fmt.Errorf("empty response")
	}

	if err := saveToken(user.ID, user.Token, url, user.Registry, user.Buildkit); err != nil {
		return err
	}

	d, err := base64.StdEncoding.DecodeString(user.Certificate)
	if err != nil {
		return fmt.Errorf("bad response: %w", err)
	}

	return ioutil.WriteFile(GetCertificatePath(), d, 0600)
}

func queryUser(ctx context.Context, client *graphql.Client, token string) (*q, error) {
	var user q
	q := fmt.Sprintf(`query {
		user {
			id,name,email,githubID,token,new,registry,buildkit,certificate
		}}`)

	req := getRequest(q, token)

	if err := client.Run(ctx, req, &user); err != nil {
		if err := client.Run(ctx, req, &user); err != nil {
			if strings.Contains(err.Error(), "Cannot query field") {
				log.Infof("query using the legacy parameters: %s", err)
				return queryUserLegacy(ctx, client, token)
			}
			return nil, fmt.Errorf("unauthorized request: %w", err)
		}
	}

	return &user, nil
}

func queryUserLegacy(ctx context.Context, client *graphql.Client, token string) (*q, error) {
	var user q
	q := fmt.Sprintf(`query {
		user {
			id,name,email,githubID,token,new
		}}`)

	req := getRequest(q, token)

	if err := client.Run(ctx, req, &user); err != nil {
		if err := client.Run(ctx, req, &user); err != nil {
			return nil, fmt.Errorf("unauthorized request: %w", err)
		}
	}

	return &user, nil
}

func authUser(ctx context.Context, client *graphql.Client, code string) (*u, error) {
	var user u
	q := fmt.Sprintf(`mutation {
		auth(code: "%s", source: "cli") {
			id,name,email,githubID,token,new,registry,buildkit,certificate
		}}`, code)

	req := graphql.NewRequest(q)
	if err := client.Run(ctx, req, &user); err != nil {
		if strings.Contains(err.Error(), "Cannot query field") {
			log.Infof("query using the legacy parameters: %s", err)
			return authUserLegacy(ctx, client, code)
		}
		return nil, fmt.Errorf("unauthorized request: %w", err)
	}

	return &user, nil
}

func authUserLegacy(ctx context.Context, client *graphql.Client, code string) (*u, error) {
	var user u
	q := fmt.Sprintf(`mutation {
	auth(code: "%s", source: "cli") {
		id,name,email,githubID,token,new
	}}`, code)

	req := graphql.NewRequest(q)
	if err := client.Run(ctx, req, &user); err != nil {
		return nil, fmt.Errorf("unauthorized request: %w", err)
	}

	return &user, nil
}

//GetToken returns the token of the authenticated user
func GetToken() (*Token, error) {
	if currentToken == nil {
		p := getTokenPath()

		b, err := ioutil.ReadFile(p)
		if err != nil {
			return nil, err
		}

		currentToken = &Token{}
		if err := json.Unmarshal(b, currentToken); err != nil {
			return nil, err
		}
	}

	return currentToken, nil
}

// GetUserID returns the userID of the authenticated user
func GetUserID() string {
	t, err := GetToken()
	if err != nil {
		return ""
	}

	return t.ID
}

// GetMachineID returns the userID of the authenticated user
func GetMachineID() string {
	t, err := GetToken()
	if err != nil {
		return ""
	}

	return t.MachineID
}

// GetURL returns the URL of the authenticated user
func GetURL() string {
	t, err := GetToken()
	if err != nil {
		return "na"
	}

	return t.URL
}

// GetRegistry returns the URL of the registry
func GetRegistry() (string, error) {
	t, err := GetToken()
	if err != nil {
		return "", errors.ErrNotLogged
	}
	if t.Registry == "" {
		if GetURL() == CloudURL {
			return CloudRegistryURL, nil
		}
		return "", errors.ErrNotLogged
	}
	return t.Registry, nil
}

// GetBuildKit returns the URL of the okteto buildkit
func GetBuildKit() (string, error) {
	t, err := GetToken()
	if err != nil {
		return "", errors.ErrNotLogged
	}
	if t.Buildkit == "" {
		if GetURL() == CloudURL {
			return CloudBuildKitURL, nil
		}
		return "", errors.ErrNotLogged
	}
	return t.Buildkit, nil
}

// GetCertificatePath returns the path  to the certificate of the okteto buildkit
func GetCertificatePath() string {
	return filepath.Join(config.GetOktetoHome(), ".ca.crt")
}

func saveToken(id, token, url, registry, buildkit string) error {
	t, err := GetToken()
	if err != nil {
		log.Debugf("bad token, re-initializing: %s", err)
		t = &Token{}
	}

	t.ID = id
	t.Token = token
	t.URL = url
	t.Buildkit = buildkit
	t.Registry = registry
	return save(t)
}

// SaveMachineID updates the token file with the machineID value
func SaveMachineID(machineID string) error {
	t, err := GetToken()
	if err != nil {
		log.Infof("bad token, re-initializing: %s", err)
		t = &Token{}
	}

	t.MachineID = machineID
	return save(t)
}

// SaveID updates the token file with the userID value
func SaveID(userID string) error {
	t, err := GetToken()
	if err != nil {
		log.Debugf("bad token, re-initializing: %s", err)
		t = &Token{}
	}

	t.ID = userID
	return save(t)
}

func save(t *Token) error {
	marshalled, err := json.Marshal(t)
	if err != nil {
		log.Infof("failed to marshal token: %s", err)
		return fmt.Errorf("Failed to generate your auth token")
	}

	p := getTokenPath()
	log.Debugf("saving token at %s", p)
	if _, err := os.Stat(p); err == nil {
		err = os.Chmod(p, 0600)
		if err != nil {
			return fmt.Errorf("couldn't change token permissions: %s", err)
		}
	}

	if err := ioutil.WriteFile(p, marshalled, 0600); err != nil {
		return fmt.Errorf("couldn't save authentication token: %s", err)
	}

	currentToken = nil
	return nil
}

func getTokenPath() string {
	return filepath.Join(config.GetOktetoHome(), tokenFile)
}
