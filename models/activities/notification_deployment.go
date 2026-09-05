// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities

import (
	"context"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
)

// CreateDeploymentReviewNotifications creates unread notifications for the given recipients about
// a deployment pending review. Notifications already existing for the same deployment are skipped.
func CreateDeploymentReviewNotifications(ctx context.Context, repo *repo_model.Repository, doerID int64, deploymentID int64, recipientIDs []int64) error {
	if len(recipientIDs) == 0 {
		return nil
	}
	deployment, err := actions_model.GetDeploymentByID(ctx, deploymentID)
	if err != nil {
		return err
	}

	var existing []*Notification
	if err := db.GetEngine(ctx).
		Where("deployment_id = ? AND source = ?", deploymentID, NotificationSourceDeployment).
		Select("user_id").
		Find(&existing); err != nil {
		return err
	}
	has := make(map[int64]bool, len(existing))
	for _, n := range existing {
		has[n.UserID] = true
	}

	notifications := make([]*Notification, 0, len(recipientIDs))
	seen := make(map[int64]bool, len(recipientIDs))
	for _, uid := range recipientIDs {
		if uid == 0 || uid == doerID || seen[uid] || has[uid] {
			continue
		}
		seen[uid] = true
		notifications = append(notifications, &Notification{
			UserID:       uid,
			RepoID:       repo.ID,
			Status:       NotificationStatusUnread,
			Source:       NotificationSourceDeployment,
			DeploymentID: deploymentID,
			CommitID:     deployment.CommitSHA,
			UpdatedBy:    doerID,
		})
	}
	if len(notifications) == 0 {
		return nil
	}
	return db.Insert(ctx, notifications)
}

// CreateDeploymentDecisionNotification notifies the run trigger user about an approval/rejection decision.
func CreateDeploymentDecisionNotification(ctx context.Context, repo *repo_model.Repository, doerID, deploymentID int64) error {
	deployment, err := actions_model.GetDeploymentByID(ctx, deploymentID)
	if err != nil {
		return err
	}
	if deployment.TriggerUserID == 0 || deployment.TriggerUserID == doerID {
		return nil
	}
	n := &Notification{
		UserID:       deployment.TriggerUserID,
		RepoID:       repo.ID,
		Status:       NotificationStatusUnread,
		Source:       NotificationSourceDeployment,
		DeploymentID: deploymentID,
		CommitID:     deployment.CommitSHA,
		UpdatedBy:    doerID,
	}
	return db.Insert(ctx, n)
}
