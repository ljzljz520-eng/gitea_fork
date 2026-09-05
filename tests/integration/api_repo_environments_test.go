// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	actions_model "gitea.dev/models/actions"
	auth_model "gitea.dev/models/auth"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	api "gitea.dev/modules/structs"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIRepoEnvironments(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	session := loginUser(t, owner.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

	envURL := "/api/v1/repos/" + repo.FullName() + "/actions/environments/production"

	t.Run("CreateEnvironment", func(t *testing.T) {
		body := api.CreateOrUpdateEnvironmentOption{
			Description:      "prod",
			BranchPolicyMode: "selected",
			BranchPatterns:   []string{"main", "release-*"},
			Reviewers:        []*api.CreateEnvironmentReviewer{{Type: "user", ID: owner.ID}},
			Exclusive:        true,
		}
		req := NewRequestWithJSON(t, "PUT", envURL, body).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)
		var env api.Environment
		DecodeJSON(t, resp, &env)
		assert.Equal(t, "production", env.Name)
		assert.Equal(t, "selected", env.BranchPolicyMode)
		assert.Equal(t, []string{"main", "release-*"}, env.BranchPatterns)
		require.Len(t, env.Reviewers, 1)
		assert.Equal(t, owner.ID, env.Reviewers[0].ID)
		assert.True(t, env.Exclusive)
	})

	t.Run("GetAndListEnvironment", func(t *testing.T) {
		req := NewRequest(t, "GET", envURL).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)
		var env api.Environment
		DecodeJSON(t, resp, &env)
		assert.Equal(t, "prod", env.Description)

		req = NewRequest(t, "GET", "/api/v1/repos/"+repo.FullName()+"/actions/environments").AddTokenAuth(token)
		resp = MakeRequest(t, req, http.StatusOK)
		var envs []api.Environment
		DecodeJSON(t, resp, &envs)
		assert.NotEmpty(t, envs)
	})

	t.Run("GetMissingEnvironment404", func(t *testing.T) {
		req := NewRequest(t, "GET", "/api/v1/repos/"+repo.FullName()+"/actions/environments/missing").AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNotFound)
	})

	t.Run("FreezeWindows", func(t *testing.T) {
		body := api.CreateFreezeWindowOption{
			Kind:     "once",
			Name:     "release freeze",
			StartAt:  time.Now().Add(time.Hour),
			EndAt:    time.Now().Add(2 * time.Hour),
			Timezone: "UTC",
		}
		req := NewRequestWithJSON(t, "POST", envURL+"/freeze-windows", body).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusCreated)
		var w api.FreezeWindow
		DecodeJSON(t, resp, &w)
		assert.Equal(t, "once", w.Kind)
		assert.Equal(t, "release freeze", w.Name)

		req = NewRequest(t, "GET", envURL+"/freeze-windows").AddTokenAuth(token)
		resp = MakeRequest(t, req, http.StatusOK)
		var windows []api.FreezeWindow
		DecodeJSON(t, resp, &windows)
		require.Len(t, windows, 1)

		req = NewRequest(t, "DELETE", fmt.Sprintf("%s/freeze-windows/%d", envURL, w.ID)).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNoContent)
	})

	t.Run("LockAndUnlock", func(t *testing.T) {
		req := NewRequestWithJSON(t, "PUT", envURL+"/lock", api.LockEnvironmentOption{Reason: "incident"}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)
		var env api.Environment
		DecodeJSON(t, resp, &env)
		assert.True(t, env.Locked)

		req = NewRequest(t, "DELETE", envURL+"/lock").AddTokenAuth(token)
		resp = MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &env)
		assert.False(t, env.Locked)
	})

	t.Run("Variables", func(t *testing.T) {
		url := envURL + "/variables/DEPLOY_VAR"
		req := NewRequestWithJSON(t, "POST", envURL+"/variables", api.CreateEnvironmentVariableOption{
			Name: "DEPLOY_VAR", Data: "v1",
		}).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusCreated)

		req = NewRequestWithJSON(t, "PUT", url, api.UpdateEnvironmentVariableOption{Data: "v2"}).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusOK)

		req = NewRequest(t, "GET", url).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)
		var v api.EnvironmentVariable
		DecodeJSON(t, resp, &v)
		assert.Equal(t, "v2", v.Data)

		req = NewRequest(t, "GET", envURL+"/variables").AddTokenAuth(token)
		resp = MakeRequest(t, req, http.StatusOK)
		var vars []api.EnvironmentVariable
		DecodeJSON(t, resp, &vars)
		assert.Len(t, vars, 1)

		req = NewRequest(t, "DELETE", url).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNoContent)
	})

	t.Run("Secrets", func(t *testing.T) {
		url := envURL + "/secrets/DEPLOY_SECRET"
		req := NewRequestWithJSON(t, "PUT", url, api.CreateOrUpdateSecretOption{Data: "s3cr3t"}).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusCreated)

		req = NewRequest(t, "GET", envURL+"/secrets").AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)
		var secrets []api.EnvironmentSecret
		DecodeJSON(t, resp, &secrets)
		require.Len(t, secrets, 1)
		assert.Equal(t, "DEPLOY_SECRET", secrets[0].Name)

		// stored encrypted
		stored := unittest.AssertExistsAndLoadBean(t, &actions_model.EnvironmentSecret{Name: "DEPLOY_SECRET"})
		assert.NotEqual(t, "s3cr3t", stored.Data)

		req = NewRequest(t, "DELETE", url).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNoContent)
	})

	t.Run("DeleteEnvironment", func(t *testing.T) {
		req := NewRequest(t, "DELETE", envURL).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNoContent)

		req = NewRequest(t, "GET", envURL).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusNotFound)
	})
}
