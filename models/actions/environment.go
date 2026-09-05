// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

// BranchPolicyMode controls which refs may deploy to an environment.
type BranchPolicyMode int

const (
	// BranchPolicyAll: every branch/tag may deploy (default).
	BranchPolicyAll BranchPolicyMode = iota
	// BranchPolicyProtected: only protected branches may deploy.
	BranchPolicyProtected
	// BranchPolicySelected: only refs matching the configured glob patterns may deploy.
	BranchPolicySelected
)

// EnvironmentReviewerType distinguishes user reviewers from team reviewers.
type EnvironmentReviewerType int

const (
	EnvironmentReviewerUser EnvironmentReviewerType = iota
	EnvironmentReviewerTeam
)

// FreezeWindowKind selects one-shot vs recurring freeze windows.
type FreezeWindowKind int

const (
	// FreezeWindowOnce is a window with a fixed start and end.
	FreezeWindowOnce FreezeWindowKind = iota
	// FreezeWindowRecurring is a weekly window with weekdays, time-of-day and duration.
	FreezeWindowRecurring
)

const EnvironmentNameMaxLength = 255

// Environment is a named deployment target of a repository with its protection configuration.
type Environment struct {
	ID          int64  `xorm:"pk autoincr"`
	RepoID      int64  `xorm:"UNIQUE(repo_name) INDEX NOT NULL"`
	Name        string `xorm:"UNIQUE(repo_name) NOT NULL"`
	Description string `xorm:"TEXT"`

	BranchPolicyMode BranchPolicyMode `xorm:"SMALLINT NOT NULL DEFAULT 0"`
	// Exclusive enforces serialized deployments: at most one deployment job runs at a time.
	Exclusive bool `xorm:"NOT NULL DEFAULT FALSE"`

	// Manual deployment lock, blocks every deployment while set.
	Locked       bool               `xorm:"NOT NULL DEFAULT FALSE"`
	LockedBy     int64              `xorm:"NOT NULL DEFAULT 0"`
	LockedReason string             `xorm:"VARCHAR(255) NOT NULL DEFAULT ''"`
	LockedUnix   timeutil.TimeStamp `xorm:"NOT NULL DEFAULT 0"`

	CreatedUnix timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
}

// EnvironmentReviewer is a required reviewer (user or team) for deployments to an environment.
type EnvironmentReviewer struct {
	ID           int64                   `xorm:"pk autoincr"`
	EnvID        int64                   `xorm:"UNIQUE(env_reviewer) INDEX NOT NULL"`
	ReviewerType EnvironmentReviewerType `xorm:"UNIQUE(env_reviewer) SMALLINT NOT NULL"`
	ReviewerID   int64                   `xorm:"UNIQUE(env_reviewer) NOT NULL"`
	CreatedUnix  timeutil.TimeStamp      `xorm:"created"`
}

// EnvironmentAllowedBranch is a glob pattern of refs allowed to deploy when BranchPolicyMode is selected.
type EnvironmentAllowedBranch struct {
	ID          int64              `xorm:"pk autoincr"`
	EnvID       int64              `xorm:"UNIQUE(env_pattern) INDEX NOT NULL"`
	Pattern     string             `xorm:"UNIQUE(env_pattern) VARCHAR(255) NOT NULL"`
	CreatedUnix timeutil.TimeStamp `xorm:"created"`
}

// EnvironmentFreezeWindow is a time window during which deployments are held back.
type EnvironmentFreezeWindow struct {
	ID    int64            `xorm:"pk autoincr"`
	EnvID int64            `xorm:"INDEX NOT NULL"`
	Name  string           `xorm:"VARCHAR(255) NOT NULL DEFAULT ''"`
	Kind  FreezeWindowKind `xorm:"SMALLINT NOT NULL DEFAULT 0"`

	// Once kind
	StartUnix timeutil.TimeStamp `xorm:"NOT NULL DEFAULT 0"`
	EndUnix   timeutil.TimeStamp `xorm:"NOT NULL DEFAULT 0"`

	// Recurring kind: Weekdays is a bitmask of Go time.Weekday (Sunday=0 .. Saturday=6),
	// StartTime is "HH:MM" in Timezone and the window lasts DurationMinutes.
	Weekdays        int    `xorm:"NOT NULL DEFAULT 0"`
	StartTime       string `xorm:"VARCHAR(5) NOT NULL DEFAULT ''"`
	DurationMinutes int64  `xorm:"NOT NULL DEFAULT 0"`
	Timezone        string `xorm:"VARCHAR(64) NOT NULL DEFAULT ''"`

	CreatedBy   int64              `xorm:"NOT NULL DEFAULT 0"`
	CreatedUnix timeutil.TimeStamp `xorm:"created"`
}

func init() {
	db.RegisterModel(new(Environment))
	db.RegisterModel(new(EnvironmentReviewer))
	db.RegisterModel(new(EnvironmentAllowedBranch))
	db.RegisterModel(new(EnvironmentFreezeWindow))
}

// CreateEnvironment inserts a new environment.
func CreateEnvironment(ctx context.Context, env *Environment) (*Environment, error) {
	if len(env.Name) == 0 || len(env.Name) > EnvironmentNameMaxLength {
		return nil, util.NewInvalidArgumentErrorf("invalid environment name length")
	}
	return env, db.Insert(ctx, env)
}

// GetEnvironmentByID loads an environment by its ID.
func GetEnvironmentByID(ctx context.Context, envID int64) (*Environment, error) {
	env, has, err := db.GetByID[Environment](ctx, envID)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("environment %d: %w", envID, util.ErrNotExist)
	}
	return env, nil
}

// GetEnvironmentByName loads an environment of a repo by name, case-insensitively.
func GetEnvironmentByName(ctx context.Context, repoID int64, name string) (*Environment, error) {
	var env Environment
	has, err := db.GetEngine(ctx).
		Where("repo_id = ? AND LOWER(name) = LOWER(?)", repoID, name).
		Get(&env)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("environment %q in repo %d: %w", name, repoID, util.ErrNotExist)
	}
	return &env, nil
}

type FindEnvironmentsOpts struct {
	db.ListOptions
	RepoID int64
}

func (opts FindEnvironmentsOpts) ToConds() builder.Cond {
	return builder.Eq{"repo_id": opts.RepoID}
}

func (opts FindEnvironmentsOpts) ToOrders() string {
	return "name ASC"
}

// FindEnvironments lists environments of a repository.
func FindEnvironments(ctx context.Context, opts FindEnvironmentsOpts) ([]*Environment, error) {
	return db.Find[Environment](ctx, opts)
}

// UpdateEnvironmentCols persists the given columns of an environment.
func UpdateEnvironmentCols(ctx context.Context, env *Environment, cols ...string) error {
	_, err := db.GetEngine(ctx).ID(env.ID).Cols(cols...).Update(env)
	return err
}

// SyncEnvironmentReviewers replaces the reviewer set of an environment within the given session.
func SyncEnvironmentReviewers(ctx context.Context, envID int64, reviewers []*EnvironmentReviewer) error {
	if err := deleteByEnvID(ctx, new(EnvironmentReviewer), envID); err != nil {
		return err
	}
	for _, r := range reviewers {
		r.EnvID = envID
	}
	if len(reviewers) > 0 {
		return db.Insert(ctx, reviewers)
	}
	return nil
}

// GetEnvironmentReviewers loads the reviewers of an environment.
func GetEnvironmentReviewers(ctx context.Context, envID int64) ([]*EnvironmentReviewer, error) {
	var reviewers []*EnvironmentReviewer
	return reviewers, db.GetEngine(ctx).Where("env_id = ?", envID).Find(&reviewers)
}

// SyncEnvironmentBranches replaces the allowed-branch patterns of an environment.
func SyncEnvironmentBranches(ctx context.Context, envID int64, patterns []string) error {
	if err := deleteByEnvID(ctx, new(EnvironmentAllowedBranch), envID); err != nil {
		return err
	}
	branches := make([]*EnvironmentAllowedBranch, 0, len(patterns))
	seen := make(map[string]bool, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || seen[pattern] {
			continue
		}
		seen[pattern] = true
		branches = append(branches, &EnvironmentAllowedBranch{EnvID: envID, Pattern: pattern})
	}
	if len(branches) > 0 {
		return db.Insert(ctx, branches)
	}
	return nil
}

// GetEnvironmentBranches loads the allowed-branch patterns of an environment.
func GetEnvironmentBranches(ctx context.Context, envID int64) ([]*EnvironmentAllowedBranch, error) {
	var branches []*EnvironmentAllowedBranch
	return branches, db.GetEngine(ctx).Where("env_id = ?", envID).Find(&branches)
}

// CreateFreezeWindow inserts a freeze window.
func CreateFreezeWindow(ctx context.Context, w *EnvironmentFreezeWindow) (*EnvironmentFreezeWindow, error) {
	if w.Kind == FreezeWindowOnce && (!w.StartUnix.IsZero() && w.EndUnix <= w.StartUnix) {
		return nil, util.NewInvalidArgumentErrorf("freeze window end must be after start")
	}
	if w.Kind == FreezeWindowRecurring && (w.StartTime == "" || w.DurationMinutes <= 0) {
		return nil, util.NewInvalidArgumentErrorf("recurring freeze window needs start time and duration")
	}
	return w, db.Insert(ctx, w)
}

// GetFreezeWindow loads a freeze window by ID.
func GetFreezeWindow(ctx context.Context, windowID int64) (*EnvironmentFreezeWindow, error) {
	w, has, err := db.GetByID[EnvironmentFreezeWindow](ctx, windowID)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("freeze window %d: %w", windowID, util.ErrNotExist)
	}
	return w, nil
}

// GetFreezeWindows loads all freeze windows of an environment.
func GetFreezeWindows(ctx context.Context, envID int64) ([]*EnvironmentFreezeWindow, error) {
	var windows []*EnvironmentFreezeWindow
	return windows, db.GetEngine(ctx).Where("env_id = ?", envID).OrderBy("id DESC").Find(&windows)
}

// DeleteFreezeWindow deletes a freeze window.
func DeleteFreezeWindow(ctx context.Context, windowID int64) error {
	_, err := db.DeleteByID[EnvironmentFreezeWindow](ctx, windowID)
	return err
}

// DeleteEnvironment removes an environment and its configuration. Deployment history is kept.
func DeleteEnvironment(ctx context.Context, env *Environment) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		for _, bean := range []any{
			new(EnvironmentReviewer),
			new(EnvironmentAllowedBranch),
			new(EnvironmentFreezeWindow),
			new(EnvironmentVariable),
			new(EnvironmentSecret),
		} {
			if err := deleteByEnvID(ctx, bean, env.ID); err != nil {
				return err
			}
		}
		_, err := db.DeleteByID[Environment](ctx, env.ID)
		return err
	})
}

// deleteByEnvID deletes every row of the given environment-scoped bean type for the environment.
func deleteByEnvID(ctx context.Context, bean any, envID int64) error {
	_, err := db.GetEngine(ctx).Where("env_id = ?", envID).Delete(bean)
	return err
}

// IsActive reports whether the freeze window covers now.
func (w *EnvironmentFreezeWindow) IsActive(now time.Time) bool {
	switch w.Kind {
	case FreezeWindowOnce:
		return !now.Before(w.StartUnix.AsTime()) && !now.After(w.EndUnix.AsTime())
	case FreezeWindowRecurring:
		loc := time.Local
		if w.Timezone != "" {
			if l, err := time.LoadLocation(w.Timezone); err == nil {
				loc = l
			}
		}
		local := now.In(loc)
		if w.Weekdays&(1<<int(local.Weekday())) == 0 {
			return false
		}
		hh, mm, ok := parseHHMM(w.StartTime)
		if !ok {
			return false
		}
		start := time.Date(local.Year(), local.Month(), local.Day(), hh, mm, 0, 0, loc)
		end := start.Add(time.Duration(w.DurationMinutes) * time.Minute)
		return !now.Before(start) && now.Before(end)
	}
	return false
}

// ActiveFreezeWindow returns the first currently active window of the given list, if any.
func ActiveFreezeWindow(windows []*EnvironmentFreezeWindow, now time.Time) *EnvironmentFreezeWindow {
	for _, w := range windows {
		if w.IsActive(now) {
			return w
		}
	}
	return nil
}

func parseHHMM(s string) (int, int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	hh := int(s[0]-'0')*10 + int(s[1]-'0')
	mm := int(s[3]-'0')*10 + int(s[4]-'0')
	if hh > 23 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}
