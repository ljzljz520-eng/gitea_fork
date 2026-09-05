// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"errors"
	"net/http"
	"strconv"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	organization "gitea.dev/models/organization"
	user_model "gitea.dev/models/user"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"
	"gitea.dev/modules/web"
	"gitea.dev/routers/api/v1/utils"
	actions_service "gitea.dev/services/actions"
	"gitea.dev/services/context"
)

func branchPolicyModeToString(mode actions_model.BranchPolicyMode) string {
	switch mode {
	case actions_model.BranchPolicyProtected:
		return "protected"
	case actions_model.BranchPolicySelected:
		return "selected"
	default:
		return "all"
	}
}

func parseBranchPolicyMode(s string) actions_model.BranchPolicyMode {
	switch s {
	case "protected":
		return actions_model.BranchPolicyProtected
	case "selected":
		return actions_model.BranchPolicySelected
	default:
		return actions_model.BranchPolicyAll
	}
}

// loadEnvByName loads the environment named in the path, scoped to the repo
func loadEnvByName(ctx *context.APIContext) *actions_model.Environment {
	env, err := actions_model.GetEnvironmentByName(ctx, ctx.Repo.Repository.ID, ctx.PathParam("environment_name"))
	if err != nil {
		if errors.Is(err, util.ErrNotExist) {
			ctx.APIError(http.StatusNotFound, "environment does not exist")
		} else {
			ctx.APIErrorInternal(err)
		}
		return nil
	}
	return env
}

func toAPIReviewers(ctx *context.APIContext, env *actions_model.Environment) []*api.EnvironmentReviewer {
	reviewers, err := actions_model.GetEnvironmentReviewers(ctx, env.ID)
	if err != nil {
		ctx.APIErrorInternal(err)
		return nil
	}
	out := make([]*api.EnvironmentReviewer, 0, len(reviewers))
	for _, r := range reviewers {
		apiR := &api.EnvironmentReviewer{
			ID: r.ReviewerID,
		}
		if r.ReviewerType == actions_model.EnvironmentReviewerTeam {
			apiR.Type = "team"
			if team, err := organization.GetTeamByID(ctx, r.ReviewerID); err == nil {
				apiR.Name = team.Name
			}
		} else {
			apiR.Type = "user"
			if u, err := user_model.GetUserByID(ctx, r.ReviewerID); err == nil {
				apiR.Name = u.Name
			}
		}
		out = append(out, apiR)
	}
	return out
}

func toAPIEnvironment(ctx *context.APIContext, env *actions_model.Environment) *api.Environment {
	patterns, err := actions_model.GetEnvironmentBranches(ctx, env.ID)
	if err != nil {
		ctx.APIErrorInternal(err)
		return nil
	}
	patternNames := make([]string, 0, len(patterns))
	for _, p := range patterns {
		patternNames = append(patternNames, p.Pattern)
	}
	return &api.Environment{
		ID:               env.ID,
		Name:             env.Name,
		Description:      env.Description,
		BranchPolicyMode: branchPolicyModeToString(env.BranchPolicyMode),
		BranchPatterns:   patternNames,
		Reviewers:        toAPIReviewers(ctx, env),
		Exclusive:        env.Exclusive,
		Locked:           env.Locked,
		LockedReason:     env.LockedReason,
		CreatedAt:        env.CreatedUnix.AsTime(),
		UpdatedAt:        env.UpdatedUnix.AsTime(),
	}
}

// ListEnvironments lists the repository environments
func ListEnvironments(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/actions/environments repository listActionsEnvironments
	// ---
	// summary: List repository environments
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/EnvironmentList"
	envs, err := db.Find[actions_model.Environment](ctx, actions_model.FindEnvironmentsOpts{
		ListOptions: utils.GetListOptions(ctx),
		RepoID:      ctx.Repo.Repository.ID,
	})
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	out := make([]*api.Environment, 0, len(envs))
	for _, env := range envs {
		out = append(out, toAPIEnvironment(ctx, env))
	}
	ctx.JSON(http.StatusOK, out)
}

// GetEnvironment gets a single environment with its protection configuration
func GetEnvironment(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/actions/environments/{environment_name} repository getActionsEnvironment
	// ---
	// summary: Get a repository environment
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/Environment"
	//   "404":
	//     "$ref": "#/responses/notFound"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	ctx.JSON(http.StatusOK, toAPIEnvironment(ctx, env))
}

// UpdateEnvironment creates or updates an environment
func UpdateEnvironment(ctx *context.APIContext) {
	// swagger:operation PUT /repos/{owner}/{repo}/actions/environments/{environment_name} repository updateActionsEnvironment
	// ---
	// summary: Create or update a repository environment
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   description: environment configuration
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/CreateOrUpdateEnvironmentOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/Environment"
	//   "422":
	//     "$ref": "#/responses/validationError"
	form := web.GetForm[*api.CreateOrUpdateEnvironmentOption](ctx)
	refs := make([]actions_service.EnvironmentReviewerRef, 0, len(form.Reviewers))
	for _, r := range form.Reviewers {
		ref := actions_service.EnvironmentReviewerRef{ID: r.ID}
		if r.Type == "team" {
			ref.Type = actions_model.EnvironmentReviewerTeam
		} else {
			ref.Type = actions_model.EnvironmentReviewerUser
		}
		refs = append(refs, ref)
	}
	env, err := actions_service.SaveEnvironment(ctx, ctx.Repo.Repository, ctx.PathParam("environment_name"), actions_service.EnvironmentSaveOptions{
		Description:      form.Description,
		BranchPolicyMode: parseBranchPolicyMode(form.BranchPolicyMode),
		BranchPatterns:   form.BranchPatterns,
		Reviewers:        refs,
		Exclusive:        form.Exclusive,
	})
	if err != nil {
		if errors.Is(err, util.ErrInvalidArgument) {
			ctx.APIError(http.StatusUnprocessableEntity, err.Error())
		} else {
			ctx.APIErrorInternal(err)
		}
		return
	}
	ctx.JSON(http.StatusOK, toAPIEnvironment(ctx, env))
}

// DeleteEnvironment deletes an environment and its configuration
func DeleteEnvironment(ctx *context.APIContext) {
	// swagger:operation DELETE /repos/{owner}/{repo}/actions/environments/{environment_name} repository deleteActionsEnvironment
	// ---
	// summary: Delete a repository environment
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "404":
	//     "$ref": "#/responses/notFound"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	if err := actions_model.DeleteEnvironment(ctx, env); err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

// LockEnvironment locks an environment, blocking all deployments
func LockEnvironment(ctx *context.APIContext) {
	// swagger:operation PUT /repos/{owner}/{repo}/actions/environments/{environment_name}/lock repository lockActionsEnvironment
	// ---
	// summary: Lock a repository environment
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/LockEnvironmentOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/Environment"
	//   "404":
	//     "$ref": "#/responses/notFound"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	form := web.GetForm[*api.LockEnvironmentOption](ctx)
	if err := actions_service.LockEnvironment(ctx, ctx.Repo.Repository, ctx.Doer, env.ID, form.Reason); err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	updated, err := actions_model.GetEnvironmentByID(ctx, env.ID)
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	ctx.JSON(http.StatusOK, toAPIEnvironment(ctx, updated))
}

// UnlockEnvironment unlocks an environment and resumes blocked deployments
func UnlockEnvironment(ctx *context.APIContext) {
	// swagger:operation DELETE /repos/{owner}/{repo}/actions/environments/{environment_name}/lock repository unlockActionsEnvironment
	// ---
	// summary: Unlock a repository environment
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/Environment"
	//   "404":
	//     "$ref": "#/responses/notFound"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	if err := actions_service.UnlockEnvironment(ctx, ctx.Repo.Repository, env.ID); err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	updated, err := actions_model.GetEnvironmentByID(ctx, env.ID)
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	ctx.JSON(http.StatusOK, toAPIEnvironment(ctx, updated))
}

func toAPIFreezeWindow(w *actions_model.EnvironmentFreezeWindow) *api.FreezeWindow {
	out := &api.FreezeWindow{
		ID:              w.ID,
		Name:            w.Name,
		Timezone:        w.Timezone,
		StartTime:       w.StartTime,
		DurationMinutes: w.DurationMinutes,
		CreatedAt:       w.CreatedUnix.AsTime(),
	}
	if w.Kind == actions_model.FreezeWindowRecurring {
		out.Kind = "recurring"
		weekdays := make([]int, 0, 7)
		for d := 0; d < 7; d++ {
			if w.Weekdays&(1<<d) != 0 {
				weekdays = append(weekdays, d)
			}
		}
		out.Weekdays = weekdays
	} else {
		out.Kind = "once"
		out.StartAt = w.StartUnix.AsTime()
		out.EndAt = w.EndUnix.AsTime()
	}
	return out
}

// ListFreezeWindows lists freeze windows of an environment
func ListFreezeWindows(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/actions/environments/{environment_name}/freeze-windows repository listActionsFreezeWindows
	// ---
	// summary: List environment freeze windows
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/FreezeWindowList"
	//   "404":
	//     "$ref": "#/responses/notFound"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	windows, err := actions_model.GetFreezeWindows(ctx, env.ID)
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	out := make([]*api.FreezeWindow, 0, len(windows))
	for _, w := range windows {
		out = append(out, toAPIFreezeWindow(w))
	}
	ctx.JSON(http.StatusOK, out)
}

// CreateFreezeWindow creates a freeze window for an environment
func CreateFreezeWindow(ctx *context.APIContext) {
	// swagger:operation POST /repos/{owner}/{repo}/actions/environments/{environment_name}/freeze-windows repository createActionsFreezeWindow
	// ---
	// summary: Create an environment freeze window
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/CreateFreezeWindowOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/FreezeWindow"
	//   "422":
	//     "$ref": "#/responses/validationError"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	form := web.GetForm[*api.CreateFreezeWindowOption](ctx)
	w := &actions_model.EnvironmentFreezeWindow{
		Name:            form.Name,
		Timezone:        form.Timezone,
		StartTime:       form.StartTime,
		DurationMinutes: form.DurationMinutes,
		CreatedBy:       ctx.Doer.ID,
	}
	if form.Kind == "recurring" {
		w.Kind = actions_model.FreezeWindowRecurring
		for _, d := range form.Weekdays {
			if d >= 0 && d < 7 {
				w.Weekdays |= 1 << d
			}
		}
	} else {
		w.Kind = actions_model.FreezeWindowOnce
		w.StartUnix = timeutil.TimeStamp(form.StartAt.Unix())
		w.EndUnix = timeutil.TimeStamp(form.EndAt.Unix())
	}
	if _, err := actions_service.CreateEnvironmentFreezeWindow(ctx, ctx.Repo.Repository, env.Name, w); err != nil {
		if errors.Is(err, util.ErrInvalidArgument) {
			ctx.APIError(http.StatusUnprocessableEntity, err.Error())
		} else {
			ctx.APIErrorInternal(err)
		}
		return
	}
	ctx.JSON(http.StatusCreated, toAPIFreezeWindow(w))
}

// DeleteFreezeWindow deletes a freeze window
func DeleteFreezeWindow(ctx *context.APIContext) {
	// swagger:operation DELETE /repos/{owner}/{repo}/actions/environments/{environment_name}/freeze-windows/{window_id} repository deleteActionsFreezeWindow
	// ---
	// summary: Delete an environment freeze window
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// - name: window_id
	//   in: path
	//   description: id of the freeze window
	//   type: integer
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "404":
	//     "$ref": "#/responses/notFound"
	windowID := ctx.PathParamInt64("window_id")
	if _, err := actions_model.GetFreezeWindow(ctx, windowID); err != nil {
		ctx.APIError(http.StatusNotFound, "freeze window does not exist")
		return
	}
	if err := actions_model.DeleteFreezeWindow(ctx, windowID); err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

// ListEnvironmentVariables lists variables of an environment
func ListEnvironmentVariables(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/actions/environments/{environment_name}/variables repository listActionsEnvironmentVariables
	// ---
	// summary: List environment variables
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/EnvironmentVariableList"
	//   "404":
	//     "$ref": "#/responses/notFound"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	variables, err := actions_model.FindEnvironmentVariables(ctx, actions_model.FindEnvironmentVariablesOpts{EnvID: env.ID})
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	out := make([]*api.EnvironmentVariable, 0, len(variables))
	for _, v := range variables {
		out = append(out, &api.EnvironmentVariable{
			Name:        v.Name,
			Data:        v.Data,
			Description: v.Description,
			CreatedAt:   v.CreatedUnix.AsTime(),
			UpdatedAt:   v.UpdatedUnix.AsTime(),
		})
	}
	ctx.JSON(http.StatusOK, out)
}

// GetEnvironmentVariable gets a single variable of an environment
func GetEnvironmentVariable(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/actions/environments/{environment_name}/variables/{variablename} repository getActionsEnvironmentVariable
	// ---
	// summary: Get an environment variable
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// - name: variablename
	//   in: path
	//   description: name of the variable
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/EnvironmentVariable"
	//   "404":
	//     "$ref": "#/responses/notFound"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	vars, err := actions_model.FindEnvironmentVariables(ctx, actions_model.FindEnvironmentVariablesOpts{
		EnvID: env.ID,
		Name:  ctx.PathParam("variablename"),
	})
	if err != nil || len(vars) == 0 {
		ctx.APIError(http.StatusNotFound, "variable does not exist")
		return
	}
	v := vars[0]
	ctx.JSON(http.StatusOK, &api.EnvironmentVariable{Name: v.Name, Data: v.Data, Description: v.Description})
}

// CreateEnvironmentVariable creates a variable in an environment
func CreateEnvironmentVariable(ctx *context.APIContext) {
	// swagger:operation POST /repos/{owner}/{repo}/actions/environments/{environment_name}/variables repository createActionsEnvironmentVariable
	// ---
	// summary: Create an environment variable
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/CreateEnvironmentVariableOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/EnvironmentVariable"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "409":
	//     "$ref": "#/responses/error"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	form := web.GetForm[*api.CreateEnvironmentVariableOption](ctx)
	if existing, err := actions_model.FindEnvironmentVariables(ctx, actions_model.FindEnvironmentVariablesOpts{
		EnvID: env.ID,
		Name:  form.Name,
	}); err == nil && len(existing) > 0 {
		ctx.APIError(http.StatusConflict, "variable already exists")
		return
	}
	v, err := actions_model.InsertEnvironmentVariable(ctx, env.RepoID, env.ID, form.Name, form.Data, form.Description)
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	ctx.JSON(http.StatusCreated, &api.EnvironmentVariable{Name: v.Name, Data: v.Data, Description: v.Description, CreatedAt: v.CreatedUnix.AsTime()})
}

// UpdateEnvironmentVariable updates a variable in an environment
func UpdateEnvironmentVariable(ctx *context.APIContext) {
	// swagger:operation PUT /repos/{owner}/{repo}/actions/environments/{environment_name}/variables/{variablename} repository updateActionsEnvironmentVariable
	// ---
	// summary: Update an environment variable
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// - name: variablename
	//   in: path
	//   description: name of the variable
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/UpdateEnvironmentVariableOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/EnvironmentVariable"
	//   "404":
	//     "$ref": "#/responses/notFound"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	vars, err := actions_model.FindEnvironmentVariables(ctx, actions_model.FindEnvironmentVariablesOpts{
		EnvID: env.ID,
		Name:  ctx.PathParam("variablename"),
	})
	if err != nil || len(vars) == 0 {
		ctx.APIError(http.StatusNotFound, "variable does not exist")
		return
	}
	form := web.GetForm[*api.UpdateEnvironmentVariableOption](ctx)
	v := vars[0]
	v.Data = form.Data
	v.Description = form.Description
	if ok, err := actions_model.UpdateEnvironmentVariableCols(ctx, v, "data", "description"); err != nil || !ok {
		ctx.APIErrorInternal(err)
		return
	}
	ctx.JSON(http.StatusOK, &api.EnvironmentVariable{Name: v.Name, Data: v.Data, Description: v.Description})
}

// DeleteEnvironmentVariable deletes a variable from an environment
func DeleteEnvironmentVariable(ctx *context.APIContext) {
	// swagger:operation DELETE /repos/{owner}/{repo}/actions/environments/{environment_name}/variables/{variablename} repository deleteActionsEnvironmentVariable
	// ---
	// summary: Delete an environment variable
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// - name: variablename
	//   in: path
	//   description: name of the variable
	//   type: string
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "404":
	//     "$ref": "#/responses/notFound"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	vars, err := actions_model.FindEnvironmentVariables(ctx, actions_model.FindEnvironmentVariablesOpts{
		EnvID: env.ID,
		Name:  ctx.PathParam("variablename"),
	})
	if err != nil || len(vars) == 0 {
		ctx.APIError(http.StatusNotFound, "variable does not exist")
		return
	}
	if err := actions_model.DeleteEnvironmentVariable(ctx, vars[0].ID); err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

// ListEnvironmentSecrets lists secret names of an environment
func ListEnvironmentSecrets(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/actions/environments/{environment_name}/secrets repository listActionsEnvironmentSecrets
	// ---
	// summary: List environment secrets
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/EnvironmentSecretList"
	//   "404":
	//     "$ref": "#/responses/notFound"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	secrets, err := actions_model.FindEnvironmentSecrets(ctx, actions_model.FindEnvironmentSecretsOpts{EnvID: env.ID})
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	out := make([]*api.EnvironmentSecret, 0, len(secrets))
	for _, s := range secrets {
		out = append(out, &api.EnvironmentSecret{Name: s.Name, UpdatedAt: s.CreatedUnix.AsTime()})
	}
	ctx.JSON(http.StatusOK, out)
}

// SetEnvironmentSecret creates or updates an environment secret
func SetEnvironmentSecret(ctx *context.APIContext) {
	// swagger:operation PUT /repos/{owner}/{repo}/actions/environments/{environment_name}/secrets/{secretname} repository setActionsEnvironmentSecret
	// ---
	// summary: Create or update an environment secret
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// - name: secretname
	//   in: path
	//   description: name of the secret
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/CreateOrUpdateSecretOption"
	// responses:
	//   "201":
	//     description: created
	//   "204":
	//     description: updated
	//   "404":
	//     "$ref": "#/responses/notFound"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	form := web.GetForm[*api.CreateOrUpdateSecretOption](ctx)
	name := ctx.PathParam("secretname")
	existing, err := actions_model.FindEnvironmentSecrets(ctx, actions_model.FindEnvironmentSecretsOpts{EnvID: env.ID, Name: name})
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	if len(existing) == 0 {
		if _, err := actions_model.InsertEncryptedEnvironmentSecret(ctx, env.RepoID, env.ID, name, form.Data, ""); err != nil {
			ctx.APIErrorInternal(err)
			return
		}
		ctx.Status(http.StatusCreated)
		return
	}
	if err := actions_model.UpdateEnvironmentSecret(ctx, existing[0].ID, form.Data, existing[0].Description); err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

// DeleteEnvironmentSecret deletes a secret from an environment
func DeleteEnvironmentSecret(ctx *context.APIContext) {
	// swagger:operation DELETE /repos/{owner}/{repo}/actions/environments/{environment_name}/secrets/{secretname} repository deleteActionsEnvironmentSecret
	// ---
	// summary: Delete an environment secret
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// - name: secretname
	//   in: path
	//   description: name of the secret
	//   type: string
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "404":
	//     "$ref": "#/responses/notFound"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	secrets, err := actions_model.FindEnvironmentSecrets(ctx, actions_model.FindEnvironmentSecretsOpts{
		EnvID: env.ID,
		Name:  ctx.PathParam("secretname"),
	})
	if err != nil || len(secrets) == 0 {
		ctx.APIError(http.StatusNotFound, "secret does not exist")
		return
	}
	if err := actions_model.DeleteEnvironmentSecret(ctx, secrets[0].ID); err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func toAPIDeployment(ctx *context.APIContext, d *actions_model.Deployment) *api.Deployment {
	job, err := actions_model.GetRunJobByRepoAndID(ctx, d.RepoID, d.RunJobID)
	status := string(actions_model.DeploymentStatusPending)
	if err == nil {
		status = string(d.DisplayStatus(job.Status))
	}
	out := &api.Deployment{
		ID:                d.ID,
		Environment:       d.EnvName,
		EnvironmentID:     d.EnvID,
		RunID:             d.RunID,
		RunJobID:          d.RunJobID,
		Ref:               d.Ref,
		Sha:               d.CommitSHA,
		TriggerUserID:     d.TriggerUserID,
		URL:               d.URL,
		Status:            status,
		HasReviewDecision: d.ReviewStatus != actions_model.DeploymentReviewPending,
		ReviewerID:        d.ReviewerID,
		ReviewComment:     d.ReviewComment,
		CreatedAt:         d.CreatedUnix.AsTime(),
		ReviewedAt:        d.ReviewedUnix.AsTime(),
	}
	if u, err := user_model.GetUserByID(ctx, d.TriggerUserID); err == nil {
		out.TriggerUser = u.Name
	}
	return out
}

// ListEnvironmentDeployments lists deployments into an environment
func ListEnvironmentDeployments(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/actions/environments/{environment_name}/deployments repository listActionsEnvironmentDeployments
	// ---
	// summary: List environment deployments
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: environment_name
	//   in: path
	//   description: name of the environment
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/DeploymentList"
	//   "404":
	//     "$ref": "#/responses/notFound"
	env := loadEnvByName(ctx)
	if ctx.Written() {
		return
	}
	listDeployments(ctx, actions_model.FindDeploymentsOpts{
		ListOptions: utils.GetListOptions(ctx),
		RepoID:      ctx.Repo.Repository.ID,
		EnvID:       env.ID,
	})
}

// ListDeployments lists deployments across all environments of a repository
func ListDeployments(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/actions/deployments repository listActionsDeployments
	// ---
	// summary: List repository deployments
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/DeploymentList"
	listDeployments(ctx, actions_model.FindDeploymentsOpts{
		ListOptions: utils.GetListOptions(ctx),
		RepoID:      ctx.Repo.Repository.ID,
	})
}

func listDeployments(ctx *context.APIContext, opts actions_model.FindDeploymentsOpts) {
	deployments, err := actions_model.FindDeployments(ctx, opts)
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	out := make([]*api.Deployment, 0, len(deployments))
	for _, d := range deployments {
		out = append(out, toAPIDeployment(ctx, d))
	}
	ctx.JSON(http.StatusOK, out)
}

// ReviewDeployment approves or rejects a pending deployment
func ReviewDeployment(ctx *context.APIContext) {
	// swagger:operation POST /repos/{owner}/{repo}/actions/deployments/{id}/reviews repository reviewActionsDeployment
	// ---
	// summary: Approve or reject a pending deployment
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repository
	//   type: string
	//   required: true
	// - name: id
	//   in: path
	//   description: id of the deployment
	//   type: integer
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/CreateDeploymentReviewOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/Deployment"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"
	deploymentID, err := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	if err != nil {
		ctx.APIError(http.StatusBadRequest, "invalid deployment id")
		return
	}
	form := web.GetForm[*api.CreateDeploymentReviewOption](ctx)
	deployment, err := actions_service.ReviewDeployment(ctx, ctx.Repo.Repository, ctx.Doer, deploymentID, form.Event == "approved", form.Comment)
	if err != nil {
		var denied actions_service.ErrEnvironmentReviewDenied
		if errors.As(err, &denied) {
			ctx.APIError(http.StatusForbidden, denied.Reason)
		} else if errors.Is(err, util.ErrNotExist) {
			ctx.APIError(http.StatusNotFound, err.Error())
		} else if errors.Is(err, util.ErrInvalidArgument) {
			ctx.APIError(http.StatusUnprocessableEntity, err.Error())
		} else {
			ctx.APIErrorInternal(err)
		}
		return
	}
	ctx.JSON(http.StatusOK, toAPIDeployment(ctx, deployment))
}
