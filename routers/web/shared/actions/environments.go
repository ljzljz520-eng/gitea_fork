// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"errors"
	"strings"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	organization "gitea.dev/models/organization"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/log"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"
	"gitea.dev/modules/web"
	actions_service "gitea.dev/services/actions"
	"gitea.dev/services/context"
)

const (
	settingsTemplateEnvironments = "repo/settings/actions_environments"
	settingsTemplateEnvironment  = "repo/settings/actions_environment"
)

type environmentNameForm struct {
	Name        string `form:"name"`
	Description string `form:"description"`
}

type environmentForm struct {
	Description      string `form:"description"`
	BranchPolicyMode string `form:"branch_policy_mode"`
	BranchPatterns   string `form:"branch_patterns"`
	Reviewers        string `form:"reviewers"`
	Exclusive        bool   `form:"exclusive"`
}

type lockEnvironmentForm struct {
	Reason string `form:"reason"`
}

type freezeWindowForm struct {
	Kind            string `form:"kind"`
	Name            string `form:"name"`
	StartAt         string `form:"start_at"`
	EndAt           string `form:"end_at"`
	Weekdays        []int  `form:"weekdays"`
	StartTime       string `form:"start_time"`
	DurationMinutes int    `form:"duration_minutes"`
	Timezone        string `form:"timezone"`
}

type variableEnvironmentForm struct {
	Name        string `form:"name"`
	Data        string `form:"data"`
	Description string `form:"description"`
}

type secretEnvironmentForm struct {
	Name string `form:"name"`
	Data string `form:"data"`
}

// Environments renders the repository environments list.
func Environments(ctx *context.Context) {
	environments, err := db.Find[actions_model.Environment](ctx, actions_model.FindEnvironmentsOpts{RepoID: ctx.Repo.Repository.ID})
	if err != nil {
		ctx.ServerError("FindEnvironments", err)
		return
	}
	type envRow struct {
		Env              *actions_model.Environment
		ReviewerCount    int
		FreezeActive     bool
		ActiveWindowName string
		DeploymentCount  int64
	}
	now := time.Now()
	rows := make([]*envRow, 0, len(environments))
	for _, env := range environments {
		reviewers, err := actions_model.GetEnvironmentReviewers(ctx, env.ID)
		if err != nil {
			ctx.ServerError("GetEnvironmentReviewers", err)
			return
		}
		windows, err := actions_model.GetFreezeWindows(ctx, env.ID)
		if err != nil {
			ctx.ServerError("GetFreezeWindows", err)
			return
		}
		count, err := db.Count[actions_model.Deployment](ctx, actions_model.FindDeploymentsOpts{EnvID: env.ID})
		if err != nil {
			ctx.ServerError("CountDeployments", err)
			return
		}
		row := &envRow{Env: env, ReviewerCount: len(reviewers), DeploymentCount: count}
		if w := actions_model.ActiveFreezeWindow(windows, now); w != nil {
			row.FreezeActive = true
			row.ActiveWindowName = w.Name
		}
		rows = append(rows, row)
	}
	ctx.Data["Rows"] = rows
	ctx.Data["PageIsActionsSettingsEnvironments"] = true
	ctx.HTML(200, settingsTemplateEnvironments)
}

// EnvironmentSettings renders a single environment's protection configuration.
func EnvironmentSettings(ctx *context.Context) {
	env := getPathEnvironment(ctx)
	if env == nil {
		return
	}
	reviewers, err := actions_model.GetEnvironmentReviewers(ctx, env.ID)
	if err != nil {
		ctx.ServerError("GetEnvironmentReviewers", err)
		return
	}
	branches, err := actions_model.GetEnvironmentBranches(ctx, env.ID)
	if err != nil {
		ctx.ServerError("GetEnvironmentBranches", err)
		return
	}
	windows, err := actions_model.GetFreezeWindows(ctx, env.ID)
	if err != nil {
		ctx.ServerError("GetFreezeWindows", err)
		return
	}
	variables, err := actions_model.FindEnvironmentVariables(ctx, actions_model.FindEnvironmentVariablesOpts{EnvID: env.ID})
	if err != nil {
		ctx.ServerError("FindEnvironmentVariables", err)
		return
	}
	secrets, err := actions_model.FindEnvironmentSecrets(ctx, actions_model.FindEnvironmentSecretsOpts{EnvID: env.ID})
	if err != nil {
		ctx.ServerError("FindEnvironmentSecrets", err)
		return
	}
	deployments, _, err := db.FindAndCount[actions_model.Deployment](ctx, actions_model.FindDeploymentsOpts{
		RepoID:      env.RepoID,
		EnvID:       env.ID,
		ListOptions: db.ListOptions{PageSize: 20},
	})
	if err != nil {
		ctx.ServerError("FindDeployments", err)
		return
	}

	reviewerNames := make([]string, 0, len(reviewers))
	for _, r := range reviewers {
		if r.ReviewerType == actions_model.EnvironmentReviewerUser {
			if u, err := user_model.GetUserByID(ctx, r.ReviewerID); err == nil {
				reviewerNames = append(reviewerNames, u.Name)
			}
		} else {
			if t, err := organization.GetTeamByID(ctx, r.ReviewerID); err == nil {
				reviewerNames = append(reviewerNames, "@"+t.Name)
			}
		}
	}

	ctx.Data["Env"] = env
	ctx.Data["ReviewerNames"] = strings.Join(reviewerNames, ", ")
	ctx.Data["Branches"] = branches
	ctx.Data["FreezeWindows"] = windows
	ctx.Data["EnvVariables"] = variables
	ctx.Data["EnvSecrets"] = secrets
	ctx.Data["Deployments"] = deployments
	ctx.Data["PageIsActionsSettingsEnvironments"] = true
	ctx.HTML(200, settingsTemplateEnvironment)
}

func getPathEnvironment(ctx *context.Context) *actions_model.Environment {
	env, err := actions_model.GetEnvironmentByName(ctx, ctx.Repo.Repository.ID, ctx.PathParam("environment"))
	if err != nil {
		if errors.Is(err, util.ErrNotExist) {
			ctx.NotFound(err)
		} else {
			ctx.ServerError("GetEnvironmentByName", err)
		}
		return nil
	}
	return env
}

func environmentLink(ctx *context.Context, name string) string {
	return ctx.Repo.RepoLink + "/settings/actions/environments/" + util.PathEscapeSegments(name)
}

// EnvironmentCreate creates an environment (name + description only).
func EnvironmentCreate(ctx *context.Context) {
	form := web.GetForm[*environmentNameForm](ctx)
	if form.Name == "" || len(form.Name) > actions_model.EnvironmentNameMaxLength {
		handleEnvActionError(ctx, util.NewInvalidArgumentErrorf("invalid environment name"))
		return
	}
	if _, err := actions_model.CreateEnvironment(ctx, &actions_model.Environment{
		RepoID:      ctx.Repo.Repository.ID,
		Name:        form.Name,
		Description: form.Description,
	}); err != nil {
		handleEnvActionError(ctx, err)
		return
	}
	ctx.JSONRedirect(environmentLink(ctx, form.Name))
}

// EnvironmentUpdate updates the protection configuration of an environment.
func EnvironmentUpdate(ctx *context.Context) {
	env := getPathEnvironment(ctx)
	if env == nil {
		return
	}
	form := web.GetForm[*environmentForm](ctx)

	mode := actions_model.BranchPolicyAll
	switch form.BranchPolicyMode {
	case "protected":
		mode = actions_model.BranchPolicyProtected
	case "selected":
		mode = actions_model.BranchPolicySelected
	}

	refs := resolveReviewers(ctx, ctx.Repo.Repository, form.Reviewers)
	if ctx.Written() {
		return
	}

	updated, err := actions_service.SaveEnvironment(ctx, ctx.Repo.Repository, env.Name, actions_service.EnvironmentSaveOptions{
		Description:      form.Description,
		BranchPolicyMode: mode,
		BranchPatterns:   strings.Split(form.BranchPatterns, "\n"),
		Reviewers:        refs,
		Exclusive:        form.Exclusive,
	})
	if err != nil {
		handleEnvActionError(ctx, err)
		return
	}
	ctx.JSONRedirect(environmentLink(ctx, updated.Name))
}

// EnvironmentDelete deletes an environment.
func EnvironmentDelete(ctx *context.Context) {
	env := getPathEnvironment(ctx)
	if env == nil {
		return
	}
	if err := actions_model.DeleteEnvironment(ctx, env); err != nil {
		ctx.ServerError("DeleteEnvironment", err)
		return
	}
	ctx.JSONRedirect(ctx.Repo.RepoLink + "/settings/actions/environments")
}

// EnvironmentLock locks an environment.
func EnvironmentLock(ctx *context.Context) {
	env := getPathEnvironment(ctx)
	if env == nil {
		return
	}
	form := web.GetForm[*lockEnvironmentForm](ctx)
	if err := actions_service.LockEnvironment(ctx, ctx.Repo.Repository, ctx.Doer, env.ID, form.Reason); err != nil {
		ctx.ServerError("LockEnvironment", err)
		return
	}
	ctx.JSONRedirect(environmentLink(ctx, env.Name))
}

// EnvironmentUnlock unlocks an environment.
func EnvironmentUnlock(ctx *context.Context) {
	env := getPathEnvironment(ctx)
	if env == nil {
		return
	}
	if err := actions_service.UnlockEnvironment(ctx, ctx.Repo.Repository, env.ID); err != nil {
		ctx.ServerError("UnlockEnvironment", err)
		return
	}
	ctx.JSONRedirect(environmentLink(ctx, env.Name))
}

// FreezeWindowCreate adds a freeze window to an environment.
func FreezeWindowCreate(ctx *context.Context) {
	env := getPathEnvironment(ctx)
	if env == nil {
		return
	}
	form := web.GetForm[*freezeWindowForm](ctx)
	w := &actions_model.EnvironmentFreezeWindow{
		Name:        form.Name,
		Timezone:    form.Timezone,
		StartTime:   form.StartTime,
		CreatedBy:   ctx.Doer.ID,
	}
	if form.Kind == "recurring" {
		w.Kind = actions_model.FreezeWindowRecurring
		w.DurationMinutes = int64(form.DurationMinutes)
		for _, d := range form.Weekdays {
			if d >= 0 && d <= 6 {
				w.Weekdays |= 1 << d
			}
		}
	} else {
		w.Kind = actions_model.FreezeWindowOnce
		start, err1 := time.ParseInLocation("2006-01-02T15:04", form.StartAt, time.Local)
		end, err2 := time.ParseInLocation("2006-01-02T15:04", form.EndAt, time.Local)
		if err1 != nil || err2 != nil {
			handleEnvActionError(ctx, util.NewInvalidArgumentErrorf("invalid window times"))
			return
		}
		w.StartUnix = timeutil.TimeStamp(start.Unix())
		w.EndUnix = timeutil.TimeStamp(end.Unix())
	}
	if _, err := actions_service.CreateEnvironmentFreezeWindow(ctx, ctx.Repo.Repository, env.Name, w); err != nil {
		handleEnvActionError(ctx, err)
		return
	}
	ctx.JSONRedirect(environmentLink(ctx, env.Name))
}

// FreezeWindowDelete deletes a freeze window.
func FreezeWindowDelete(ctx *context.Context) {
	env := getPathEnvironment(ctx)
	if env == nil {
		return
	}
	windowID := ctx.PathParamInt64("window")
	if err := actions_model.DeleteFreezeWindow(ctx, windowID); err != nil {
		ctx.ServerError("DeleteFreezeWindow", err)
		return
	}
	ctx.JSONRedirect(environmentLink(ctx, env.Name))
}

// EnvironmentVariableCreate adds or updates a variable.
func EnvironmentVariableCreate(ctx *context.Context) {
	env := getPathEnvironment(ctx)
	if env == nil {
		return
	}
	form := web.GetForm[*variableEnvironmentForm](ctx)
	existing, _ := actions_model.FindEnvironmentVariables(ctx, actions_model.FindEnvironmentVariablesOpts{EnvID: env.ID, Name: form.Name})
	if len(existing) == 0 {
		if _, err := actions_model.InsertEnvironmentVariable(ctx, env.RepoID, env.ID, form.Name, form.Data, form.Description); err != nil {
			handleEnvActionError(ctx, err)
			return
		}
	} else {
		v := existing[0]
		v.Data = form.Data
		v.Description = form.Description
		if _, err := actions_model.UpdateEnvironmentVariableCols(ctx, v, "data", "description"); err != nil {
			ctx.ServerError("UpdateEnvironmentVariable", err)
			return
		}
	}
	ctx.JSONRedirect(environmentLink(ctx, env.Name))
}

// EnvironmentVariableDelete deletes a variable.
func EnvironmentVariableDelete(ctx *context.Context) {
	env := getPathEnvironment(ctx)
	if env == nil {
		return
	}
	variables, _ := actions_model.FindEnvironmentVariables(ctx, actions_model.FindEnvironmentVariablesOpts{EnvID: env.ID, Name: ctx.PathParam("variablename")})
	if len(variables) == 0 {
		ctx.NotFound(util.ErrNotExist)
		return
	}
	if err := actions_model.DeleteEnvironmentVariable(ctx, variables[0].ID); err != nil {
		ctx.ServerError("DeleteEnvironmentVariable", err)
		return
	}
	ctx.JSONRedirect(environmentLink(ctx, env.Name))
}

// EnvironmentSecretSet creates or updates a secret.
func EnvironmentSecretSet(ctx *context.Context) {
	env := getPathEnvironment(ctx)
	if env == nil {
		return
	}
	form := web.GetForm[*secretEnvironmentForm](ctx)
	existing, _ := actions_model.FindEnvironmentSecrets(ctx, actions_model.FindEnvironmentSecretsOpts{EnvID: env.ID, Name: form.Name})
	if len(existing) == 0 {
		if _, err := actions_model.InsertEncryptedEnvironmentSecret(ctx, env.RepoID, env.ID, form.Name, form.Data, ""); err != nil {
			handleEnvActionError(ctx, err)
			return
		}
	} else if err := actions_model.UpdateEnvironmentSecret(ctx, existing[0].ID, form.Data, existing[0].Description); err != nil {
		ctx.ServerError("UpdateEnvironmentSecret", err)
		return
	}
	ctx.JSONRedirect(environmentLink(ctx, env.Name))
}

// EnvironmentSecretDelete deletes a secret.
func EnvironmentSecretDelete(ctx *context.Context) {
	env := getPathEnvironment(ctx)
	if env == nil {
		return
	}
	secrets, _ := actions_model.FindEnvironmentSecrets(ctx, actions_model.FindEnvironmentSecretsOpts{EnvID: env.ID, Name: ctx.PathParam("secretname")})
	if len(secrets) == 0 {
		ctx.NotFound(util.ErrNotExist)
		return
	}
	if err := actions_model.DeleteEnvironmentSecret(ctx, secrets[0].ID); err != nil {
		ctx.ServerError("DeleteEnvironmentSecret", err)
		return
	}
	ctx.JSONRedirect(environmentLink(ctx, env.Name))
}

func handleEnvActionError(ctx *context.Context, err error) {
	if errors.Is(err, util.ErrInvalidArgument) {
		ctx.Flash.Error(err.Error())
		ctx.JSONRedirect(ctx.Req.URL.String())
		return
	}
	ctx.ServerError("environment action", err)
}

// resolveReviewers parses comma-separated user names and @team names into reviewer refs.
func resolveReviewers(ctx *context.Context, repo *repo_model.Repository, text string) []actions_service.EnvironmentReviewerRef {
	refs := make([]actions_service.EnvironmentReviewerRef, 0)
	for _, part := range strings.Split(text, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "@") {
			teamName := strings.TrimPrefix(name, "@")
			team, err := organization.GetTeam(ctx, repo.OwnerID, teamName)
			if err != nil {
				log.Error("resolve reviewer team %q: %v", teamName, err)
				continue
			}
			refs = append(refs, actions_service.EnvironmentReviewerRef{Type: actions_model.EnvironmentReviewerTeam, ID: team.ID})
		} else {
			u, err := user_model.GetUserByName(ctx, name)
			if err != nil {
				log.Error("resolve reviewer user %q: %v", name, err)
				continue
			}
			refs = append(refs, actions_service.EnvironmentReviewerRef{Type: actions_model.EnvironmentReviewerUser, ID: u.ID})
		}
	}
	return refs
}
