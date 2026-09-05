// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"testing"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetVariablesOfJobPrecedence(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := context.Background()

	run := &ActionRun{RepoID: 1, Title: "vars-precedence", Index: 1, Ref: "refs/heads/main"}
	require.NoError(t, db.Insert(ctx, run))

	// repo-level variables
	_, err := InsertVariable(ctx, 0, 1, "COMMON", "repo-value", "")
	require.NoError(t, err)
	_, err = InsertVariable(ctx, 0, 1, "REPOONLY", "repo", "")
	require.NoError(t, err)

	env, err := CreateEnvironment(ctx, &Environment{RepoID: 1, Name: "vars-test"})
	require.NoError(t, err)
	_, err = InsertEnvironmentVariable(ctx, 1, env.ID, "COMMON", "env-value", "")
	require.NoError(t, err)
	_, err = InsertEnvironmentVariable(ctx, 1, env.ID, "ENVONLY", "env", "")
	require.NoError(t, err)

	// job targeting the environment: env variables present and override repo level
	got, err := GetVariablesOfJob(ctx, run, env.Name)
	require.NoError(t, err)
	assert.Equal(t, "env-value", got["COMMON"], "environment variables take precedence")
	assert.Equal(t, "repo", got["REPOONLY"])
	assert.Equal(t, "env", got["ENVONLY"])

	// job without an environment: no environment-scoped variables
	got, err = GetVariablesOfJob(ctx, run, "")
	require.NoError(t, err)
	assert.Equal(t, "repo-value", got["COMMON"])
	assert.NotContains(t, got, "ENVONLY")

	// job targeting a non-existent environment: run variables only
	got, err = GetVariablesOfJob(ctx, run, "missing-env")
	require.NoError(t, err)
	assert.Equal(t, "repo-value", got["COMMON"])
	assert.NotContains(t, got, "ENVONLY")
}
