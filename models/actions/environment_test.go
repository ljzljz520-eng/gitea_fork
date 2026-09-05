// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"testing"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFreezeWindowOnceIsActive(t *testing.T) {
	start := timeutil.TimeStamp(1_000_000)
	end := timeutil.TimeStamp(1_000_600)
	w := &EnvironmentFreezeWindow{Kind: FreezeWindowOnce, StartUnix: start, EndUnix: end}

	cases := []struct {
		when int64
		want bool
	}{
		{start.AsTime().Add(-time.Second).Unix(), false},
		{start.AsTime().Unix(), true}, // boundary inclusive
		{end.AsTime().Unix(), true},   // boundary inclusive
		{end.AsTime().Add(time.Second).Unix(), false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, w.IsActive(time.Unix(c.when, 0)), "ts=%d", c.when)
	}
}

func TestFreezeWindowRecurringIsActive(t *testing.T) {
	// Wednesday 10:00-11:00 UTC
	wednesday := time.Wednesday
	w := &EnvironmentFreezeWindow{
		Kind:            FreezeWindowRecurring,
		Weekdays:        1 << wednesday,
		StartTime:       "10:00",
		DurationMinutes: 60,
		Timezone:        "UTC",
	}
	// 2026-01-07 is a Wednesday
	inWindow := time.Date(2026, 1, 7, 10, 30, 0, 0, time.UTC)
	beforeWindow := time.Date(2026, 1, 7, 9, 59, 0, 0, time.UTC)
	atEnd := time.Date(2026, 1, 7, 11, 0, 0, 0, time.UTC) // end exclusive
	tuesday := time.Date(2026, 1, 6, 10, 30, 0, 0, time.UTC)

	assert.True(t, w.IsActive(inWindow))
	assert.False(t, w.IsActive(beforeWindow))
	assert.False(t, w.IsActive(atEnd))
	assert.False(t, w.IsActive(tuesday))

	// Timezone shifts the window: 10:00 UTC+2 is 08:00 UTC, so 08:30 UTC is active in that tz.
	wTz := *w
	wTz.Timezone = "Europe/Paris"                               // UTC+1 in January (CET)
	parisWindow := time.Date(2026, 1, 7, 9, 30, 0, 0, time.UTC) // 10:30 CET
	assert.True(t, wTz.IsActive(parisWindow))
}

func TestEnvironmentCRUD(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := context.Background()

	env, err := CreateEnvironment(ctx, &Environment{
		RepoID:           1,
		Name:             "Production",
		Description:      "prod env",
		BranchPolicyMode: BranchPolicyProtected,
	})
	require.NoError(t, err)
	assert.NotZero(t, env.ID)

	// case-insensitive lookup
	got, err := GetEnvironmentByName(ctx, 1, "production")
	require.NoError(t, err)
	assert.Equal(t, env.ID, got.ID)
	assert.Equal(t, BranchPolicyProtected, got.BranchPolicyMode)

	_, err = GetEnvironmentByName(ctx, 1, "missing")
	assert.True(t, errors.Is(err, util.ErrNotExist))

	// list
	envs, err := db.Find[Environment](ctx, FindEnvironmentsOpts{RepoID: 1})
	require.NoError(t, err)
	assert.Len(t, envs, 1)

	// reviewer + branch sync
	require.NoError(t, SyncEnvironmentReviewers(ctx, env.ID, []*EnvironmentReviewer{
		{ReviewerType: EnvironmentReviewerUser, ReviewerID: 2},
		{ReviewerType: EnvironmentReviewerTeam, ReviewerID: 1},
	}))
	reviewers, err := GetEnvironmentReviewers(ctx, env.ID)
	require.NoError(t, err)
	assert.Len(t, reviewers, 2)

	// re-sync replaces the set
	require.NoError(t, SyncEnvironmentReviewers(ctx, env.ID, []*EnvironmentReviewer{
		{ReviewerType: EnvironmentReviewerUser, ReviewerID: 3},
	}))
	reviewers, err = GetEnvironmentReviewers(ctx, env.ID)
	require.NoError(t, err)
	assert.Len(t, reviewers, 1)
	assert.Equal(t, int64(3), reviewers[0].ReviewerID)

	require.NoError(t, SyncEnvironmentBranches(ctx, env.ID, []string{"release-*", "main", "release-*"}))
	branches, err := GetEnvironmentBranches(ctx, env.ID)
	require.NoError(t, err)
	assert.Len(t, branches, 2) // duplicate pattern dropped

	// freeze window
	_, err = CreateFreezeWindow(ctx, &EnvironmentFreezeWindow{
		EnvID:     env.ID,
		Name:      "holiday",
		Kind:      FreezeWindowOnce,
		StartUnix: timeutil.TimeStampNow(),
		EndUnix:   timeutil.TimeStampNow() + 3600,
		CreatedBy: 1,
	})
	require.NoError(t, err)
	windows, err := GetFreezeWindows(ctx, env.ID)
	require.NoError(t, err)
	assert.Len(t, windows, 1)

	// variables and secrets
	_, err = InsertEnvironmentVariable(ctx, 1, env.ID, "myvar", "v1", "")
	require.NoError(t, err)
	_, err = InsertEncryptedEnvironmentSecret(ctx, 1, env.ID, "mysecret", "s3cr3t", "")
	require.NoError(t, err)

	vars, err := GetEnvironmentVariablesMap(ctx, env.ID)
	require.NoError(t, err)
	assert.Equal(t, "v1", vars["MYVAR"])
	secrets, err := GetEnvironmentSecretsMap(ctx, env.ID)
	require.NoError(t, err)
	assert.Equal(t, "s3cr3t", secrets["MYSECRET"])

	// a deployment row is kept when the environment is deleted
	dep, err := CreateDeployment(ctx, &Deployment{
		RepoID:        1,
		EnvID:         env.ID,
		EnvName:       "Production",
		RunID:         1,
		RunJobID:      999,
		TriggerUserID: 2,
	})
	require.NoError(t, err)

	require.NoError(t, DeleteEnvironment(ctx, env))

	// configuration rows are gone
	reviewers, err = GetEnvironmentReviewers(ctx, env.ID)
	require.NoError(t, err)
	assert.Empty(t, reviewers)
	vars, err = GetEnvironmentVariablesMap(ctx, env.ID)
	require.NoError(t, err)
	assert.Empty(t, vars)
	secrets, err = GetEnvironmentSecretsMap(ctx, env.ID)
	require.NoError(t, err)
	assert.Empty(t, secrets)

	// deployment history survives
	kept, err := GetDeploymentByID(ctx, dep.ID)
	require.NoError(t, err)
	assert.Equal(t, "Production", kept.EnvName)
}

func TestDeploymentDisplayStatus(t *testing.T) {
	d := &Deployment{}
	assert.Equal(t, DeploymentStatusPending, d.DisplayStatus(StatusBlocked))
	assert.Equal(t, DeploymentStatusPending, d.DisplayStatus(StatusWaiting))
	assert.Equal(t, DeploymentStatusRunning, d.DisplayStatus(StatusRunning))
	assert.Equal(t, DeploymentStatusSuccess, d.DisplayStatus(StatusSuccess))
	assert.Equal(t, DeploymentStatusFailure, d.DisplayStatus(StatusFailure))
	assert.Equal(t, DeploymentStatusCancelled, d.DisplayStatus(StatusCancelled))

	d.ReviewStatus = DeploymentReviewRejected
	assert.Equal(t, DeploymentStatusRejected, d.DisplayStatus(StatusBlocked))
}
