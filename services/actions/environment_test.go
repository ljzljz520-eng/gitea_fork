// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/test"
	"gitea.dev/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupEnvGateTest(t *testing.T) (*repo_model.Repository, *actions_model.ActionRun, *actions_model.Environment, *actions_model.Deployment) {
	t.Helper()
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	env, err := actions_model.CreateEnvironment(ctx, &actions_model.Environment{
		RepoID:      repo.ID,
		Name:        "gated-" + t.Name(),
		Description: "gate test env",
	})
	require.NoError(t, err)

	run := &actions_model.ActionRun{
		Title: t.Name(), RepoID: repo.ID, OwnerID: repo.OwnerID, WorkflowID: "test.yaml", Index: 1,
		TriggerUserID: 5, Ref: "refs/heads/main", CommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0",
		Event: "push", TriggerEvent: "push", Status: actions_model.StatusBlocked,
	}
	require.NoError(t, db.Insert(ctx, run))
	job := &actions_model.ActionRunJob{
		RunID: run.ID, RepoID: run.RepoID, OwnerID: run.OwnerID, CommitSHA: run.CommitSHA,
		Name: "deploy", Attempt: 1, JobID: "deploy", Status: actions_model.StatusBlocked,
		RunsOn: []string{"ubuntu-latest"}, Environment: env.Name,
	}
	require.NoError(t, db.Insert(ctx, job))
	deployment, _, err := upsertDeployment(ctx, run, job, env, "https://deploy.example.com")
	require.NoError(t, err)
	return repo, run, env, deployment
}

func TestEvaluateEnvironmentGate(t *testing.T) {
	ctx := t.Context()
	now := time.Now()

	t.Run("locked environment blocks", func(t *testing.T) {
		repo, run, env, dep := setupEnvGateTest(t)
		env.Locked = true
		env.LockedReason = "incident"
		require.NoError(t, actions_model.UpdateEnvironmentCols(ctx, env, "locked", "locked_reason"))

		reason, detail, blocked, err := evaluateEnvironmentGate(ctx, run, &actions_model.ActionRunJob{ID: dep.RunJobID}, env, dep, now)
		require.NoError(t, err)
		assert.True(t, blocked)
		assert.Equal(t, EnvGateReasonLocked, reason)
		assert.Equal(t, "incident", detail)
		_ = repo
	})

	t.Run("active freeze window blocks", func(t *testing.T) {
		_, run, env, dep := setupEnvGateTest(t)
		err := db.Insert(ctx, &actions_model.EnvironmentFreezeWindow{
			EnvID: env.ID, Name: "holiday", Kind: actions_model.FreezeWindowOnce,
			StartUnix: timeutil.TimeStampNow() - 60, EndUnix: timeutil.TimeStampNow() + 3600,
		})
		require.NoError(t, err)

		reason, detail, blocked, err := evaluateEnvironmentGate(ctx, run, &actions_model.ActionRunJob{ID: dep.RunJobID}, env, dep, now)
		require.NoError(t, err)
		assert.True(t, blocked)
		assert.Equal(t, EnvGateReasonFrozen, reason)
		assert.Equal(t, "holiday", detail)
	})

	t.Run("expired freeze window does not block", func(t *testing.T) {
		_, run, env, dep := setupEnvGateTest(t)
		err := db.Insert(ctx, &actions_model.EnvironmentFreezeWindow{
			EnvID: env.ID, Kind: actions_model.FreezeWindowOnce,
			StartUnix: timeutil.TimeStampNow() - 7200, EndUnix: timeutil.TimeStampNow() - 3600,
		})
		require.NoError(t, err)

		_, _, blocked, err := evaluateEnvironmentGate(ctx, run, &actions_model.ActionRunJob{ID: dep.RunJobID}, env, dep, now)
		require.NoError(t, err)
		assert.False(t, blocked)
	})

	t.Run("selected branch policy mismatch blocks", func(t *testing.T) {
		_, run, env, dep := setupEnvGateTest(t)
		env.BranchPolicyMode = actions_model.BranchPolicySelected
		require.NoError(t, actions_model.UpdateEnvironmentCols(ctx, env, "branch_policy_mode"))
		require.NoError(t, actions_model.SyncEnvironmentBranches(ctx, env.ID, []string{"release-*"}))

		reason, _, blocked, err := evaluateEnvironmentGate(ctx, run, &actions_model.ActionRunJob{ID: dep.RunJobID}, env, dep, now)
		require.NoError(t, err)
		assert.True(t, blocked)
		assert.Equal(t, EnvGateReasonBranchPolicy, reason)
	})

	t.Run("selected branch policy match passes", func(t *testing.T) {
		_, run, env, dep := setupEnvGateTest(t)
		env.BranchPolicyMode = actions_model.BranchPolicySelected
		require.NoError(t, actions_model.UpdateEnvironmentCols(ctx, env, "branch_policy_mode"))
		require.NoError(t, actions_model.SyncEnvironmentBranches(ctx, env.ID, []string{"main"}))

		_, _, blocked, err := evaluateEnvironmentGate(ctx, run, &actions_model.ActionRunJob{ID: dep.RunJobID}, env, dep, now)
		require.NoError(t, err)
		assert.False(t, blocked)
	})

	t.Run("protected branch policy blocks tags", func(t *testing.T) {
		_, run, env, dep := setupEnvGateTest(t)
		env.BranchPolicyMode = actions_model.BranchPolicyProtected
		require.NoError(t, actions_model.UpdateEnvironmentCols(ctx, env, "branch_policy_mode"))
		run.Ref = "refs/tags/v1.0"

		reason, _, blocked, err := evaluateEnvironmentGate(ctx, run, &actions_model.ActionRunJob{ID: dep.RunJobID}, env, dep, now)
		require.NoError(t, err)
		assert.True(t, blocked)
		assert.Equal(t, EnvGateReasonBranchPolicy, reason)
	})

	t.Run("required reviewers block pending deployments", func(t *testing.T) {
		_, run, env, dep := setupEnvGateTest(t)
		require.NoError(t, actions_model.SyncEnvironmentReviewers(ctx, env.ID, []*actions_model.EnvironmentReviewer{
			{ReviewerType: actions_model.EnvironmentReviewerUser, ReviewerID: 40},
		}))

		reason, _, blocked, err := evaluateEnvironmentGate(ctx, run, &actions_model.ActionRunJob{ID: dep.RunJobID}, env, dep, now)
		require.NoError(t, err)
		assert.True(t, blocked)
		assert.Equal(t, EnvGateReasonWaitingReview, reason)
	})

	t.Run("approved deployment passes reviewers gate", func(t *testing.T) {
		_, run, env, dep := setupEnvGateTest(t)
		require.NoError(t, actions_model.SyncEnvironmentReviewers(ctx, env.ID, []*actions_model.EnvironmentReviewer{
			{ReviewerType: actions_model.EnvironmentReviewerUser, ReviewerID: 40},
		}))
		dep.ReviewStatus = actions_model.DeploymentReviewApproved
		require.NoError(t, actions_model.UpdateDeploymentCols(ctx, dep, "review_status"))

		_, _, blocked, err := evaluateEnvironmentGate(ctx, run, &actions_model.ActionRunJob{ID: dep.RunJobID}, env, dep, now)
		require.NoError(t, err)
		assert.False(t, blocked)
	})

	t.Run("rejected deployment reports rejected", func(t *testing.T) {
		_, run, env, dep := setupEnvGateTest(t)
		require.NoError(t, actions_model.SyncEnvironmentReviewers(ctx, env.ID, []*actions_model.EnvironmentReviewer{
			{ReviewerType: actions_model.EnvironmentReviewerUser, ReviewerID: 40},
		}))
		dep.ReviewStatus = actions_model.DeploymentReviewRejected
		dep.ReviewComment = "no"
		require.NoError(t, actions_model.UpdateDeploymentCols(ctx, dep, "review_status", "review_comment"))

		reason, _, blocked, err := evaluateEnvironmentGate(ctx, run, &actions_model.ActionRunJob{ID: dep.RunJobID}, env, dep, now)
		require.NoError(t, err)
		assert.True(t, blocked)
		assert.Equal(t, EnvGateReasonRejected, reason)
	})

	t.Run("exclusive environment queues behind active deployment", func(t *testing.T) {
		repo, run, env, dep := setupEnvGateTest(t)
		env.Exclusive = true
		require.NoError(t, actions_model.UpdateEnvironmentCols(ctx, env, "exclusive"))
		// another job in the same environment is already waiting for a runner
		holder := &actions_model.ActionRunJob{
			RunID: run.ID + 999, RepoID: repo.ID, OwnerID: repo.OwnerID, CommitSHA: run.CommitSHA,
			Name: "holder", Attempt: 1, JobID: "deploy", Status: actions_model.StatusRunning,
			RunsOn: []string{"ubuntu-latest"}, Environment: env.Name,
		}
		require.NoError(t, db.Insert(ctx, holder))

		reason, _, blocked, err := evaluateEnvironmentGate(ctx, run, &actions_model.ActionRunJob{ID: dep.RunJobID}, env, dep, now)
		require.NoError(t, err)
		assert.True(t, blocked)
		assert.Equal(t, EnvGateReasonQueued, reason)
	})
}

func TestReEmitEnvironmentBlockedRuns(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()

	var emitted []int64
	defer test.MockVariableValue(&EmitJobsIfReadyByRun, func(runID int64) error { emitted = append(emitted, runID); return nil })()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	run := &actions_model.ActionRun{
		Title: "reemit", RepoID: repo.ID, OwnerID: repo.OwnerID, WorkflowID: "test.yaml", Index: 1,
		TriggerUserID: 5, Ref: "refs/heads/main", Event: "push", TriggerEvent: "push", Status: actions_model.StatusBlocked,
	}
	require.NoError(t, db.Insert(ctx, run))
	blockedJob := &actions_model.ActionRunJob{
		RunID: run.ID, RepoID: repo.ID, OwnerID: repo.OwnerID,
		Name: "frozen", Attempt: 1, JobID: "deploy", Status: actions_model.StatusBlocked,
		RunsOn: []string{"ubuntu-latest"}, Environment: "frozen-env",
	}
	require.NoError(t, db.Insert(ctx, blockedJob))
	// a normal blocked job without environment must not be picked up
	plainRun := &actions_model.ActionRun{
		Title: "plain", RepoID: repo.ID, OwnerID: repo.OwnerID, WorkflowID: "test.yaml", Index: 2,
		TriggerUserID: 5, Ref: "refs/heads/main", Event: "push", TriggerEvent: "push", Status: actions_model.StatusBlocked,
	}
	require.NoError(t, db.Insert(ctx, plainRun))
	plainJob := &actions_model.ActionRunJob{
		RunID: plainRun.ID, RepoID: repo.ID, OwnerID: repo.OwnerID,
		Name: "plain", Attempt: 1, JobID: "build", Status: actions_model.StatusBlocked,
		RunsOn: []string{"ubuntu-latest"},
	}
	require.NoError(t, db.Insert(ctx, plainJob))

	require.NoError(t, ReEmitEnvironmentBlockedRuns(ctx))
	assert.Equal(t, []int64{run.ID}, emitted)
}

func TestReviewDeploymentAuth(t *testing.T) {
	defer test.MockVariableValue(&EmitJobsIfReadyByRun, func(runID int64) error { return nil })()
	ctx := t.Context()

	t.Run("admin approves another user's deployment", func(t *testing.T) {
		repo, run, env, dep := setupEnvGateTest(t)
		_ = run
		require.NoError(t, actions_model.SyncEnvironmentReviewers(ctx, env.ID, []*actions_model.EnvironmentReviewer{
			{ReviewerType: actions_model.EnvironmentReviewerUser, ReviewerID: 40},
		}))
		admin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		got, err := ReviewDeployment(ctx, repo, admin, dep.ID, true, "ok")
		require.NoError(t, err)
		assert.Equal(t, actions_model.DeploymentReviewApproved, got.ReviewStatus)
		assert.Equal(t, admin.ID, got.ReviewerID)
	})

	t.Run("trigger user cannot approve own deployment", func(t *testing.T) {
		repo, _, env, dep := setupEnvGateTest(t)
		require.NoError(t, actions_model.SyncEnvironmentReviewers(ctx, env.ID, []*actions_model.EnvironmentReviewer{}))
		// trigger user is 5; they would otherwise pass as admin? user 5 is not repo admin.
		triggerUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
		_, err := ReviewDeployment(ctx, repo, triggerUser, dep.ID, true, "")
		require.Error(t, err)
		var denied ErrEnvironmentReviewDenied
		assert.ErrorAs(t, err, &denied)
	})

	t.Run("unauthorized user cannot approve", func(t *testing.T) {
		repo, _, env, dep := setupEnvGateTest(t)
		require.NoError(t, actions_model.SyncEnvironmentReviewers(ctx, env.ID, []*actions_model.EnvironmentReviewer{
			{ReviewerType: actions_model.EnvironmentReviewerUser, ReviewerID: 2},
		}))
		// user 40 has only read access and is not listed as a reviewer
		other := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 40})
		_, err := ReviewDeployment(ctx, repo, other, dep.ID, true, "")
		require.Error(t, err)
	})

	t.Run("listed reviewer with read access can approve", func(t *testing.T) {
		repo, _, env, dep := setupEnvGateTest(t)
		require.NoError(t, actions_model.SyncEnvironmentReviewers(ctx, env.ID, []*actions_model.EnvironmentReviewer{
			{ReviewerType: actions_model.EnvironmentReviewerUser, ReviewerID: 40},
		}))
		reviewer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 40})
		got, err := ReviewDeployment(ctx, repo, reviewer, dep.ID, true, "")
		require.NoError(t, err)
		assert.Equal(t, actions_model.DeploymentReviewApproved, got.ReviewStatus)
	})

	t.Run("rejection fails the blocked job", func(t *testing.T) {
		repo, _, _, dep := setupEnvGateTest(t)
		admin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		_, err := ReviewDeployment(ctx, repo, admin, dep.ID, false, "not now")
		require.NoError(t, err)

		job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: dep.RunJobID})
		assert.Equal(t, actions_model.StatusFailure, job.Status)
		updated := unittest.AssertExistsAndLoadBean(t, &actions_model.Deployment{ID: dep.ID})
		assert.Equal(t, actions_model.DeploymentReviewRejected, updated.ReviewStatus)
	})
}
