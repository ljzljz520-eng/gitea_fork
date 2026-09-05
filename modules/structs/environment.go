// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package structs

import "time"

// Environment represents an Actions deployment environment
// swagger:model
type Environment struct {
	ID               int64                  `json:"id"`
	Name             string                 `json:"name"`
	Description      string                 `json:"description"`
	BranchPolicyMode string                 `json:"branch_policy_mode"` // "all", "protected" or "selected"
	BranchPatterns   []string               `json:"branch_patterns"`
	Reviewers        []*EnvironmentReviewer `json:"reviewers"`
	Exclusive        bool                   `json:"exclusive"`
	Locked           bool                   `json:"locked"`
	LockedReason     string                 `json:"locked_reason,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
}

// EnvironmentReviewer represents a required reviewer of an environment
// swagger:model
type EnvironmentReviewer struct {
	Type string `json:"type"` // "user" or "team"
	ID   int64  `json:"id"`
	Name string `json:"name,omitempty"`
}

// CreateOrUpdateEnvironmentOption for creating or updating an environment
//
// swagger:model
type CreateOrUpdateEnvironmentOption struct {
	Description      string                       `json:"description"`
	BranchPolicyMode string                       `json:"branch_policy_mode" binding:"In(,all,protected,selected)"`
	BranchPatterns   []string                     `json:"branch_patterns"`
	Reviewers        []*CreateEnvironmentReviewer `json:"reviewers"`
	Exclusive        bool                         `json:"exclusive"`
}

// CreateEnvironmentReviewer is a reviewer entry in the environment create/update payload
// swagger:model
type CreateEnvironmentReviewer struct {
	Type string `json:"type" binding:"In(user,team)"`
	ID   int64  `json:"id"`
}

// LockEnvironmentOption holds the reason when locking an environment
// swagger:model
type LockEnvironmentOption struct {
	Reason string `json:"reason"`
}

// FreezeWindow represents a deployment freeze window of an environment
// swagger:model
type FreezeWindow struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Kind            string    `json:"kind"` // "once" or "recurring"
	StartAt         time.Time `json:"start_at,omitempty"`
	EndAt           time.Time `json:"end_at,omitempty"`
	Weekdays        []int     `json:"weekdays,omitempty"` // 0=Sunday .. 6=Saturday
	StartTime       string    `json:"start_time,omitempty"`
	DurationMinutes int64     `json:"duration_minutes,omitempty"`
	Timezone        string    `json:"timezone,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// CreateFreezeWindowOption for creating a freeze window
// swagger:model
type CreateFreezeWindowOption struct {
	Name            string    `json:"name"`
	Kind            string    `json:"kind" binding:"In(once,recurring)"`
	StartAt         time.Time `json:"start_at"`
	EndAt           time.Time `json:"end_at"`
	Weekdays        []int     `json:"weekdays"`
	StartTime       string    `json:"start_time"`
	DurationMinutes int64     `json:"duration_minutes"`
	Timezone        string    `json:"timezone"`
}

// EnvironmentVariable represents an Actions variable scoped to an environment
// swagger:model
type EnvironmentVariable struct {
	Name        string    `json:"name"`
	Data        string    `json:"data"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// CreateEnvironmentVariableOption for creating an environment variable
type CreateEnvironmentVariableOption struct {
	Name        string `json:"name" binding:"Required"`
	Data        string `json:"data"`
	Description string `json:"description"`
}

// UpdateEnvironmentVariableOption for updating an environment variable
// swagger:model
type UpdateEnvironmentVariableOption struct {
	Data        string `json:"data"`
	Description string `json:"description"`
}

// EnvironmentSecret represents an Actions secret name scoped to an environment
// swagger:model
type EnvironmentSecret struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Deployment represents an Actions deployment into an environment
// swagger:model
type Deployment struct {
	ID                int64     `json:"id"`
	Environment       string    `json:"environment"`
	EnvironmentID     int64     `json:"environment_id"`
	RunID             int64     `json:"run_id"`
	RunJobID          int64     `json:"run_job_id"`
	Ref               string    `json:"ref"`
	Sha               string    `json:"sha"`
	TriggerUserID     int64     `json:"trigger_user_id"`
	TriggerUser       string    `json:"trigger_user,omitempty"`
	URL               string    `json:"url,omitempty"`
	Status            string    `json:"status"`
	ReviewerID        int64     `json:"reviewer_id,omitempty"`
	ReviewComment     string    `json:"review_comment,omitempty"`
	HasReviewDecision bool      `json:"has_review_decision"`
	CreatedAt         time.Time `json:"created_at"`
	ReviewedAt        time.Time `json:"reviewed_at,omitempty"`
}

// CreateDeploymentReviewOption for approving or rejecting a pending deployment
// swagger:model
type CreateDeploymentReviewOption struct {
	Event   string `json:"event" binding:"Required;In(approved,rejected)"`
	Comment string `json:"comment"`
}
