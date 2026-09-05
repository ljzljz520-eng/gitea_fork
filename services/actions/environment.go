// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	actions_model "gitea.dev/models/actions"
	git_model "gitea.dev/models/git"
	organization "gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/db"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/actions/jobparser"
	"gitea.dev/modules/git"
	"gitea.dev/modules/glob"
	"gitea.dev/modules/log"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

// Gate reason codes exposed to UI/API callers.
const (
	EnvGateReasonLocked        = "locked"
	EnvGateReasonFrozen        = "frozen"
	EnvGateReasonWaitingReview = "waiting_review"
	EnvGateReasonQueued        = "queued"
	EnvGateReasonBranchPolicy  = "branch_policy"
	EnvGateReasonRejected      = "rejected"
)

// ErrEnvironmentReviewDenied indicates the doer is not allowed to review the deployment.
type ErrEnvironmentReviewDenied struct {
	Reason string
}

func (err ErrEnvironmentReviewDenied) Error() string {
	return "deployment review denied: " + err.Reason
}
func (err ErrEnvironmentReviewDenied) Unwrap() error { return util.ErrPermissionDenied }

// notifyEnvironmentDeploymentPending is wired by the notifications implementation.
var notifyEnvironmentDeploymentPending = func(ctx context.Context, repo *repo_model.Repository, deployment *actions_model.Deployment) error {
	return nil
}

// notifyEnvironmentDeploymentDecided is wired by the notifications implementation.
var notifyEnvironmentDeploymentDecided = func(ctx context.Context, repo *repo_model.Repository, deployment *actions_model.Deployment, approved bool) error {
	return nil
}

// DeploymentGateView describes the environment protection state of a run job for display.
type DeploymentGateView struct {
	EnvironmentName string
	EnvironmentURL  string
	DeploymentID    int64
	GateReason      string
	ReviewStatus    string
	CanReview       bool
}

// DescribeJobEnvironment returns the gate state of a job targeting an environment, without mutating anything.
func DescribeJobEnvironment(ctx context.Context, run *actions_model.ActionRun, job *actions_model.ActionRunJob, doer *user_model.User) (*DeploymentGateView, error) {
	if job.Environment == "" {
		return nil, nil
	}
	view := &DeploymentGateView{
		EnvironmentName: job.Environment,
		EnvironmentURL:  job.EnvironmentURL,
		ReviewStatus:    "none",
	}
	env, err := actions_model.GetEnvironmentByName(ctx, job.RepoID, job.Environment)
	if err != nil {
		if errors.Is(err, util.ErrNotExist) {
			return view, nil
		}
		return nil, err
	}
	deployment, err := actions_model.GetDeploymentByJob(ctx, job.ID)
	if err != nil && !errors.Is(err, util.ErrNotExist) {
		return nil, err
	}
	if deployment != nil {
		view.DeploymentID = deployment.ID
		switch deployment.ReviewStatus {
		case actions_model.DeploymentReviewApproved:
			view.ReviewStatus = "approved"
		case actions_model.DeploymentReviewRejected:
			view.ReviewStatus = "rejected"
		default:
			view.ReviewStatus = "pending"
		}
		if reason, _, blocked, err := evaluateEnvironmentGate(ctx, run, job, env, deployment, time.Now()); err != nil {
			return nil, err
		} else if blocked {
			view.GateReason = reason
		}
		if doer != nil && deployment.ReviewStatus == actions_model.DeploymentReviewPending {
			if err := run.LoadRepo(ctx); err != nil {
				return nil, err
			}
			canReview, err := CanReviewDeployment(ctx, run.Repo, doer, env, deployment)
			if err != nil {
				return nil, err
			}
			view.CanReview = canReview
		}
	}
	return view, nil
}

// EvaluateJobEnvironment resolves the job's `environment:` expressions, persists the result
// on the job row, and returns the evaluated name and url. An empty name means no environment.
func EvaluateJobEnvironment(ctx context.Context, run *actions_model.ActionRun, attempt *actions_model.ActionRunAttempt, job *actions_model.ActionRunJob, vars map[string]string, inputs map[string]any) (string, string, error) {
	parsedJob, err := job.ParseJob()
	if err != nil {
		return "", "", err
	}
	if !parsedJob.HasEnvironment() {
		return "", "", nil
	}
	if err := job.LoadAttributes(ctx); err != nil {
		return "", "", err
	}
	gitCtx := GenerateGiteaContext(ctx, run, attempt, job)
	jobResults, err := findJobNeedsAndFillJobResults(ctx, job)
	if err != nil {
		return "", "", err
	}
	if inputs == nil {
		inputs, err = getInputsForJob(ctx, run, job)
		if err != nil {
			return "", "", err
		}
	}
	name, url, err := jobparser.EvaluateEnvironment(job.JobID, parsedJob, gitCtx, jobResults, vars, inputs)
	if err != nil {
		return "", "", fmt.Errorf("evaluate environment of job %d: %w", job.ID, err)
	}
	if job.Environment != name || job.EnvironmentURL != url {
		job.Environment = name
		job.EnvironmentURL = url
		if _, err := actions_model.UpdateRunJob(ctx, job, nil, "environment", "environment_url"); err != nil {
			return "", "", fmt.Errorf("persist environment on job %d: %w", job.ID, err)
		}
	}
	return name, url, nil
}

// getOrCreateEnvironment looks the environment up case-insensitively and creates it on first reference.
func getOrCreateEnvironment(ctx context.Context, repoID int64, name string) (*actions_model.Environment, error) {
	env, err := actions_model.GetEnvironmentByName(ctx, repoID, name)
	if err == nil {
		return env, nil
	}
	if !errors.Is(err, util.ErrNotExist) {
		return nil, err
	}
	env = &actions_model.Environment{RepoID: repoID, Name: name}
	if _, err := actions_model.CreateEnvironment(ctx, env); err != nil {
		// A concurrent reference may have created it; fall back to a lookup.
		if existing, getErr := actions_model.GetEnvironmentByName(ctx, repoID, name); getErr == nil {
			return existing, nil
		}
		return nil, err
	}
	log.Info("environment %q auto-created in repo %d", name, repoID)
	return env, nil
}

func upsertDeployment(ctx context.Context, run *actions_model.ActionRun, job *actions_model.ActionRunJob, env *actions_model.Environment, url string) (*actions_model.Deployment, bool, error) {
	if d, err := actions_model.GetDeploymentByJob(ctx, job.ID); err == nil {
		return d, false, nil
	} else if !errors.Is(err, util.ErrNotExist) {
		return nil, false, err
	}
	d := &actions_model.Deployment{
		RepoID:        env.RepoID,
		EnvID:         env.ID,
		EnvName:       env.Name,
		RunID:         run.ID,
		RunJobID:      job.ID,
		Ref:           run.Ref,
		CommitSHA:     run.CommitSHA,
		TriggerUserID: run.TriggerUserID,
		URL:           url,
	}
	if _, err := actions_model.CreateDeployment(ctx, d); err != nil {
		return nil, false, err
	}
	return d, true, nil
}

// evaluateEnvironmentGate runs the environment protection gates in order:
// manual lock, freeze windows, deployment branch policy, required reviewers, exclusive slot.
func evaluateEnvironmentGate(ctx context.Context, run *actions_model.ActionRun, job *actions_model.ActionRunJob, env *actions_model.Environment, deployment *actions_model.Deployment, now time.Time) (string, string, bool, error) {
	if env.Locked {
		return EnvGateReasonLocked, env.LockedReason, true, nil
	}

	windows, err := actions_model.GetFreezeWindows(ctx, env.ID)
	if err != nil {
		return "", "", false, err
	}
	if w := actions_model.ActiveFreezeWindow(windows, now); w != nil {
		return EnvGateReasonFrozen, w.Name, true, nil
	}

	if rejected, reason, err := checkBranchPolicy(ctx, run, env); err != nil {
		return "", "", false, err
	} else if rejected {
		return EnvGateReasonBranchPolicy, reason, true, nil
	}

	reviewers, err := actions_model.GetEnvironmentReviewers(ctx, env.ID)
	if err != nil {
		return "", "", false, err
	}
	switch deployment.ReviewStatus {
	case actions_model.DeploymentReviewRejected:
		return EnvGateReasonRejected, deployment.ReviewComment, true, nil
	case actions_model.DeploymentReviewApproved:
		// pass through to the exclusive check
	default:
		if len(reviewers) > 0 {
			return EnvGateReasonWaitingReview, "", true, nil
		}
	}

	if env.Exclusive {
		active, err := actions_model.FindActiveDeploymentJobsInEnv(ctx, env.RepoID, env.Name, job.ID)
		if err != nil {
			return "", "", false, err
		}
		if len(active) > 0 {
			return EnvGateReasonQueued, "", true, nil
		}
	}
	return "", "", false, nil
}

// checkBranchPolicy returns rejected=true when the run's ref is not allowed to deploy.
func checkBranchPolicy(ctx context.Context, run *actions_model.ActionRun, env *actions_model.Environment) (bool, string, error) {
	if env.BranchPolicyMode == actions_model.BranchPolicyAll {
		return false, "", nil
	}
	refName := git.RefName(run.Ref)

	if env.BranchPolicyMode == actions_model.BranchPolicyProtected {
		if !refName.IsBranch() {
			return true, "protected branch policy only allows protected branches", nil
		}
		protected, err := git_model.IsBranchProtected(ctx, env.RepoID, refName.ShortName())
		if err != nil {
			return false, "", err
		}
		if !protected {
			return true, fmt.Sprintf("branch %q is not protected", refName.ShortName()), nil
		}
		return false, "", nil
	}

	// selected patterns
	branches, err := actions_model.GetEnvironmentBranches(ctx, env.ID)
	if err != nil {
		return false, "", err
	}
	shortName := refName.ShortName()
	for _, b := range branches {
		if g, gErr := glob.Compile(b.Pattern); gErr == nil && g.Match(shortName) {
			return false, "", nil
		}
	}
	return true, fmt.Sprintf("ref %q does not match any allowed branch pattern", shortName), nil
}

// gateEnvironmentJob evaluates the environment expression for a job if needed and runs the
// protection gates. It returns blocked=true when the job must stay blocked (or was terminated).
func gateEnvironmentJob(ctx context.Context, run *actions_model.ActionRun, attempt *actions_model.ActionRunAttempt, job *actions_model.ActionRunJob, vars map[string]string) (bool, error) {
	name, url, err := EvaluateJobEnvironment(ctx, run, attempt, job, vars, nil)
	if err != nil {
		return false, err
	}
	if name == "" {
		return false, nil
	}

	env, err := getOrCreateEnvironment(ctx, run.RepoID, name)
	if err != nil {
		return false, err
	}

	deployment, created, err := upsertDeployment(ctx, run, job, env, url)
	if err != nil {
		return false, err
	}

	reason, detail, blocked, err := evaluateEnvironmentGate(ctx, run, job, env, deployment, time.Now())
	if err != nil {
		return false, err
	}
	if !blocked {
		return false, nil
	}

	if reason == EnvGateReasonBranchPolicy || reason == EnvGateReasonRejected {
		// Hard denial: terminate the job and record the outcome on the deployment.
		comment := detail
		if reason == EnvGateReasonBranchPolicy {
			deployment.ReviewStatus = actions_model.DeploymentReviewRejected
			deployment.ReviewComment = "branch policy: " + comment
			deployment.ReviewedUnix = timeutil.TimeStampNow()
			if err := actions_model.UpdateDeploymentCols(ctx, deployment, "review_status", "review_comment", "reviewed_unix"); err != nil {
				return false, err
			}
		}
		job.Status = actions_model.StatusFailure
		job.Stopped = timeutil.TimeStampNow()
		if n, err := actions_model.UpdateRunJob(ctx, job, builder.Expr("status = ?", actions_model.StatusBlocked), "status", "stopped"); err != nil {
			return false, err
		} else if n == 0 {
			return true, nil // a concurrent writer advanced the job
		}
		log.Info("deployment job %d rejected for environment %q: %s", job.ID, env.Name, comment)
		return true, nil
	}

	log.Debug("deployment job %d blocked on environment %q: %s %s", job.ID, env.Name, reason, detail)
	if created && reason == EnvGateReasonWaitingReview {
		if err := run.LoadRepo(ctx); err != nil {
			return false, err
		}
		if err := notifyEnvironmentDeploymentPending(ctx, run.Repo, deployment); err != nil {
			log.Error("notify deployment %d pending: %v", deployment.ID, err)
		}
	}
	return true, nil
}

// ReEmitEnvironmentBlockedRuns re-emits runs whose jobs are blocked on an environment gate,
// so time-based states (frozen windows) are re-evaluated.
func ReEmitEnvironmentBlockedRuns(ctx context.Context) error {
	runIDs, err := actions_model.FindBlockedEnvironmentRunIDs(ctx)
	if err != nil {
		return err
	}
	for _, runID := range runIDs {
		if err := EmitJobsIfReadyByRun(runID); err != nil {
			log.Error("re-emit run %d for environment gate: %v", runID, err)
		}
	}
	return nil
}

// EnvironmentReviewerRef identifies a required reviewer of an environment.
type EnvironmentReviewerRef struct {
	Type actions_model.EnvironmentReviewerType
	ID   int64
}

// EnvironmentSaveOptions configures an environment and its protection rules.
type EnvironmentSaveOptions struct {
	Description      string
	BranchPolicyMode actions_model.BranchPolicyMode
	BranchPatterns   []string
	Reviewers        []EnvironmentReviewerRef
	Exclusive        bool
}

// SaveEnvironment creates or updates an environment and its protection configuration within a transaction.
func SaveEnvironment(ctx context.Context, repo *repo_model.Repository, name string, opt EnvironmentSaveOptions) (*actions_model.Environment, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > actions_model.EnvironmentNameMaxLength {
		return nil, util.NewInvalidArgumentErrorf("invalid environment name")
	}

	env, err := getOrCreateEnvironment(ctx, repo.ID, name)
	if err != nil {
		return nil, err
	}
	env.Description = opt.Description
	env.BranchPolicyMode = opt.BranchPolicyMode
	env.Exclusive = opt.Exclusive
	if err := actions_model.UpdateEnvironmentCols(ctx, env, "description", "branch_policy_mode", "exclusive"); err != nil {
		return nil, err
	}

	if err := db.WithTx(ctx, func(ctx context.Context) error {
		reviewers := make([]*actions_model.EnvironmentReviewer, 0, len(opt.Reviewers))
		seen := map[actions_model.EnvironmentReviewerType]map[int64]bool{}
		for _, r := range opt.Reviewers {
			if r.ID == 0 {
				continue
			}
			if seen[r.Type] == nil {
				seen[r.Type] = map[int64]bool{}
			}
			if seen[r.Type][r.ID] {
				continue
			}
			seen[r.Type][r.ID] = true
			reviewers = append(reviewers, &actions_model.EnvironmentReviewer{
				ReviewerType: r.Type,
				ReviewerID:   r.ID,
			})
		}
		if err := actions_model.SyncEnvironmentReviewers(ctx, env.ID, reviewers); err != nil {
			return err
		}
		patterns := opt.BranchPatterns
		if env.BranchPolicyMode != actions_model.BranchPolicySelected {
			patterns = nil
		}
		return actions_model.SyncEnvironmentBranches(ctx, env.ID, patterns)
	}); err != nil {
		return nil, err
	}
	return env, nil
}

// CreateEnvironmentFreezeWindow validates and creates a freeze window.
func CreateEnvironmentFreezeWindow(ctx context.Context, repo *repo_model.Repository, envName string, w *actions_model.EnvironmentFreezeWindow) (*actions_model.EnvironmentFreezeWindow, error) {
	env, err := getOrCreateEnvironment(ctx, repo.ID, envName)
	if err != nil {
		return nil, err
	}
	w.EnvID = env.ID
	return actions_model.CreateFreezeWindow(ctx, w)
}

// LockEnvironment manually locks an environment.
func LockEnvironment(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, envID int64, reason string) error {
	env, err := getEnvOfRepo(ctx, repo.ID, envID)
	if err != nil {
		return err
	}
	env.Locked = true
	env.LockedBy = doer.ID
	env.LockedReason = strings.TrimSpace(reason)
	env.LockedUnix = timeutil.TimeStampNow()
	if err := actions_model.UpdateEnvironmentCols(ctx, env, "locked", "locked_by", "locked_reason", "locked_unix"); err != nil {
		return err
	}
	return nil
}

// UnlockEnvironment unlocks an environment and wakes the runs waiting on it.
func UnlockEnvironment(ctx context.Context, repo *repo_model.Repository, envID int64) error {
	env, err := getEnvOfRepo(ctx, repo.ID, envID)
	if err != nil {
		return err
	}
	env.Locked = false
	env.LockedBy = 0
	env.LockedReason = ""
	env.LockedUnix = 0
	if err := actions_model.UpdateEnvironmentCols(ctx, env, "locked", "locked_by", "locked_reason", "locked_unix"); err != nil {
		return err
	}
	return ReEmitEnvironmentBlockedRuns(ctx)
}

func getEnvOfRepo(ctx context.Context, repoID, envID int64) (*actions_model.Environment, error) {
	env, err := actions_model.GetEnvironmentByID(ctx, envID)
	if err != nil {
		return nil, err
	}
	if env.RepoID != repoID {
		return nil, util.NewNotExistErrorf("environment %d not in repo %d", envID, repoID)
	}
	return env, nil
}

// CanReviewDeployment reports whether doer may approve or reject a pending deployment.
// Repository admins and configured reviewers (team members must have write access) may review,
// but the user who triggered the run can never review their own deployment.
func CanReviewDeployment(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, env *actions_model.Environment, deployment *actions_model.Deployment) (bool, error) {
	if deployment.TriggerUserID == doer.ID {
		return false, nil
	}
	if access_model.IsUserRepoAdmin(ctx, repo, doer) {
		return true, nil
	}

	perm, err := access_model.GetIndividualUserRepoPermission(ctx, repo, doer)
	if err != nil {
		return false, err
	}
	if !perm.CanRead(unit.TypeActions) {
		return false, nil
	}

	reviewers, err := actions_model.GetEnvironmentReviewers(ctx, env.ID)
	if err != nil {
		return false, err
	}
	var teamIDs []int64
	for _, r := range reviewers {
		if r.ReviewerType == actions_model.EnvironmentReviewerUser && r.ReviewerID == doer.ID {
			return true, nil
		}
		if r.ReviewerType == actions_model.EnvironmentReviewerTeam {
			teamIDs = append(teamIDs, r.ReviewerID)
		}
	}
	if len(teamIDs) > 0 {
		// team reviewers must be write collaborators
		if !perm.CanWrite(unit.TypeActions) {
			return false, nil
		}
		inTeam, err := organization.IsUserInTeams(ctx, doer.ID, teamIDs)
		return inTeam, err
	}
	return false, nil
}

// ReviewDeployment records an approval or rejection of a pending deployment.
func ReviewDeployment(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, deploymentID int64, approve bool, comment string) (*actions_model.Deployment, error) {
	deployment, err := actions_model.GetDeploymentByID(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	if deployment.RepoID != repo.ID {
		return nil, util.NewNotExistErrorf("deployment %d not in repo %d", deploymentID, repo.ID)
	}
	env, err := actions_model.GetEnvironmentByID(ctx, deployment.EnvID)
	if err != nil {
		return nil, err
	}
	if deployment.ReviewStatus != actions_model.DeploymentReviewPending {
		return nil, util.NewInvalidArgumentErrorf("deployment %d is not pending review", deploymentID)
	}

	canReview, err := CanReviewDeployment(ctx, repo, doer, env, deployment)
	if err != nil {
		return nil, err
	}
	if !canReview {
		reason := "you are not an authorized reviewer of this environment"
		if deployment.TriggerUserID == doer.ID {
			reason = "you cannot review your own deployment"
		}
		return nil, ErrEnvironmentReviewDenied{Reason: reason}
	}

	if approve {
		deployment.ReviewStatus = actions_model.DeploymentReviewApproved
	} else {
		deployment.ReviewStatus = actions_model.DeploymentReviewRejected
	}
	deployment.ReviewerID = doer.ID
	deployment.ReviewComment = util.TruncateRunes(comment, 500)
	deployment.ReviewedUnix = timeutil.TimeStampNow()
	if err := actions_model.UpdateDeploymentCols(ctx, deployment, "review_status", "reviewer_id", "review_comment", "reviewed_unix"); err != nil {
		return nil, err
	}

	if !approve {
		// terminate the waiting job immediately
		job, err := actions_model.GetRunJobByRepoAndID(ctx, repo.ID, deployment.RunJobID)
		if err != nil {
			return nil, err
		}
		if job.Status == actions_model.StatusBlocked {
			job.Status = actions_model.StatusFailure
			job.Stopped = timeutil.TimeStampNow()
			if _, err := actions_model.UpdateRunJob(ctx, job, builder.Expr("status = ?", actions_model.StatusBlocked), "status", "stopped"); err != nil {
				return nil, err
			}
		}
	}

	if err := notifyEnvironmentDeploymentDecided(ctx, repo, deployment, approve); err != nil {
		log.Error("notify deployment %d decision: %v", deployment.ID, err)
	}

	if approve {
		if err := EmitJobsIfReadyByRun(deployment.RunID); err != nil {
			return nil, err
		}
	}
	return deployment, nil
}
