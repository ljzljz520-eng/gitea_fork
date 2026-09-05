// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/activities"
	organization "gitea.dev/models/organization"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/log"
	notify_service "gitea.dev/services/notify"
)

func init() {
	notifyEnvironmentDeploymentPending = notifyDeploymentReviewPending
	notifyEnvironmentDeploymentDecided = notifyDeploymentReviewDecided
}

// deploymentReviewerIDs resolves the users to notify for a pending deployment review:
// configured user reviewers plus members of configured team reviewers.
func deploymentReviewerIDs(ctx context.Context, env *actions_model.Environment) ([]int64, error) {
	reviewers, err := actions_model.GetEnvironmentReviewers(ctx, env.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(reviewers))
	for _, r := range reviewers {
		if r.ReviewerType == actions_model.EnvironmentReviewerUser {
			ids = append(ids, r.ReviewerID)
			continue
		}
		members, err := organization.GetTeamMembers(ctx, &organization.SearchMembersOptions{TeamID: r.ReviewerID})
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

func notifyDeploymentReviewPending(ctx context.Context, repo *repo_model.Repository, deployment *actions_model.Deployment) error {
	env, err := actions_model.GetEnvironmentByID(ctx, deployment.EnvID)
	if err != nil {
		return err
	}
	ids, err := deploymentReviewerIDs(ctx, env)
	if err != nil {
		return err
	}
	if err := activities.CreateDeploymentReviewNotifications(ctx, repo, deployment.TriggerUserID, deployment.ID, ids); err != nil {
		return err
	}
	for _, id := range ids {
		if id != 0 && id != deployment.TriggerUserID {
			notify_service.NotificationCountChange(ctx, id)
		}
	}
	log.Info("deployment %d pending review notified to %d reviewers", deployment.ID, len(ids))
	return nil
}

func notifyDeploymentReviewDecided(ctx context.Context, repo *repo_model.Repository, deployment *actions_model.Deployment, approved bool) error {
	if err := activities.CreateDeploymentDecisionNotification(ctx, repo, deployment.ReviewerID, deployment.ID); err != nil {
		return err
	}
	if deployment.TriggerUserID != 0 && deployment.TriggerUserID != deployment.ReviewerID {
		notify_service.NotificationCountChange(ctx, deployment.TriggerUserID)
	}
	return nil
}
