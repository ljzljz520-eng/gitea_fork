// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"fmt"

	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

// DeploymentReviewStatus is the review state of a deployment.
type DeploymentReviewStatus int

const (
	// DeploymentReviewPending: the deployment waits for a required-review decision or another gate.
	DeploymentReviewPending DeploymentReviewStatus = iota
	// DeploymentReviewApproved: a reviewer approved the deployment.
	DeploymentReviewApproved
	// DeploymentReviewRejected: a reviewer rejected the deployment (or a hard policy denied it).
	DeploymentReviewRejected
)

// DeploymentStatus is the derived, user-facing status of a deployment.
type DeploymentStatus string

const (
	DeploymentStatusPending   DeploymentStatus = "pending"
	DeploymentStatusRunning   DeploymentStatus = "running"
	DeploymentStatusSuccess   DeploymentStatus = "success"
	DeploymentStatusFailure   DeploymentStatus = "failure"
	DeploymentStatusCancelled DeploymentStatus = "cancelled"
	DeploymentStatusRejected  DeploymentStatus = "rejected"
)

// Deployment records one job deploying to one environment. It is the deployment history entry.
type Deployment struct {
	ID      int64  `xorm:"pk autoincr"`
	RepoID  int64  `xorm:"INDEX NOT NULL"`
	EnvID   int64  `xorm:"INDEX NOT NULL"`
	EnvName string `xorm:"VARCHAR(255) NOT NULL DEFAULT ''"` // snapshot of the evaluated environment name

	RunID    int64 `xorm:"INDEX NOT NULL"`
	RunJobID int64 `xorm:"UNIQUE NOT NULL"`

	Ref           string `xorm:"VARCHAR(255) NOT NULL DEFAULT ''"`
	CommitSHA     string `xorm:"VARCHAR(64) NOT NULL DEFAULT ''"`
	TriggerUserID int64  `xorm:"NOT NULL DEFAULT 0"`
	URL           string `xorm:"VARCHAR(2048) NOT NULL DEFAULT ''"` // evaluated environment.url

	ReviewStatus  DeploymentReviewStatus `xorm:"SMALLINT NOT NULL DEFAULT 0"`
	ReviewerID    int64                  `xorm:"NOT NULL DEFAULT 0"`
	ReviewComment string                 `xorm:"VARCHAR(500) NOT NULL DEFAULT ''"`
	ReviewedUnix  timeutil.TimeStamp     `xorm:"NOT NULL DEFAULT 0"`

	CreatedUnix timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
}

func init() {
	db.RegisterModel(new(Deployment))
}

// DisplayStatus derives the user-facing status from the review decision and the job status.
func (d *Deployment) DisplayStatus(jobStatus Status) DeploymentStatus {
	if d.ReviewStatus == DeploymentReviewRejected {
		return DeploymentStatusRejected
	}
	switch jobStatus {
	case StatusRunning:
		return DeploymentStatusRunning
	case StatusSuccess:
		return DeploymentStatusSuccess
	case StatusFailure:
		return DeploymentStatusFailure
	case StatusCancelled, StatusCancelling:
		return DeploymentStatusCancelled
	default:
		return DeploymentStatusPending
	}
}

// CreateDeployment inserts a deployment record.
func CreateDeployment(ctx context.Context, d *Deployment) (*Deployment, error) {
	return d, db.Insert(ctx, d)
}

// GetDeploymentByID loads a deployment by ID.
func GetDeploymentByID(ctx context.Context, id int64) (*Deployment, error) {
	d, has, err := db.GetByID[Deployment](ctx, id)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("deployment %d: %w", id, util.ErrNotExist)
	}
	return d, nil
}

// GetDeploymentByJob loads the deployment of a run job, if any.
func GetDeploymentByJob(ctx context.Context, runJobID int64) (*Deployment, error) {
	var d Deployment
	has, err := db.GetEngine(ctx).Where("run_job_id = ?", runJobID).Get(&d)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("deployment for run job %d: %w", runJobID, util.ErrNotExist)
	}
	return &d, nil
}

// UpdateDeploymentCols persists the given columns of a deployment.
func UpdateDeploymentCols(ctx context.Context, d *Deployment, cols ...string) error {
	_, err := db.GetEngine(ctx).ID(d.ID).Cols(cols...).Update(d)
	return err
}

type FindDeploymentsOpts struct {
	db.ListOptions
	RepoID       int64
	EnvID        int64
	RunID        int64
	RunJobID     int64
	ReviewStatus *DeploymentReviewStatus
}

func (opts FindDeploymentsOpts) ToConds() builder.Cond {
	cond := builder.NewCond()
	if opts.RepoID > 0 {
		cond = cond.And(builder.Eq{"repo_id": opts.RepoID})
	}
	if opts.EnvID > 0 {
		cond = cond.And(builder.Eq{"env_id": opts.EnvID})
	}
	if opts.RunID > 0 {
		cond = cond.And(builder.Eq{"run_id": opts.RunID})
	}
	if opts.RunJobID > 0 {
		cond = cond.And(builder.Eq{"run_job_id": opts.RunJobID})
	}
	if opts.ReviewStatus != nil {
		cond = cond.And(builder.Eq{"review_status": *opts.ReviewStatus})
	}
	return cond
}

func (opts FindDeploymentsOpts) ToOrders() string {
	return "id DESC"
}

// FindDeployments lists deployments by the given options.
func FindDeployments(ctx context.Context, opts FindDeploymentsOpts) ([]*Deployment, error) {
	return db.Find[Deployment](ctx, opts)
}

// FindBlockedEnvironmentRunIDs returns distinct run IDs that have at least one blocked job bound to an environment.
func FindBlockedEnvironmentRunIDs(ctx context.Context) ([]int64, error) {
	var runIDs []int64
	err := db.GetEngine(ctx).Table("action_run_job").
		Cols("run_id").
		Where("status = ? AND environment != ''", StatusBlocked).
		GroupBy("run_id").
		Find(&runIDs)
	return runIDs, err
}

// FindActiveDeploymentJobsInEnv returns run jobs of the environment that are currently running,
// waiting for a runner or cancelling — i.e. holding the exclusive deployment slot.
func FindActiveDeploymentJobsInEnv(ctx context.Context, repoID int64, envName string, excludeJobID int64) ([]*ActionRunJob, error) {
	var jobs []*ActionRunJob
	err := db.GetEngine(ctx).
		Where("repo_id = ? AND environment = ? AND id != ?", repoID, envName, excludeJobID).
		In("status", StatusRunning, StatusWaiting, StatusCancelling).
		OrderBy("id ASC").
		Find(&jobs)
	return jobs, err
}

// FindBlockedEnvironmentRunIDsInEnv returns distinct run IDs with jobs blocked on the given environment.
func FindBlockedEnvironmentRunIDsInEnv(ctx context.Context, repoID int64, envName string, excludeRunID int64) ([]int64, error) {
	var runIDs []int64
	err := db.GetEngine(ctx).Table("action_run_job").
		Cols("run_id").
		Where("repo_id = ? AND environment = ? AND status = ? AND run_id != ?", repoID, envName, StatusBlocked, excludeRunID).
		GroupBy("run_id").
		Find(&runIDs)
	return runIDs, err
}
