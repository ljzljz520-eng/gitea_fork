// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"strings"
	"unicode/utf8"

	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

// EnvironmentVariable is an Actions variable scoped to a repository environment.
type EnvironmentVariable struct {
	ID          int64              `xorm:"pk autoincr"`
	RepoID      int64              `xorm:"INDEX NOT NULL"`
	EnvID       int64              `xorm:"UNIQUE(env_name) INDEX NOT NULL"`
	Name        string             `xorm:"UNIQUE(env_name) NOT NULL"`
	Data        string             `xorm:"LONGTEXT NOT NULL"`
	Description string             `xorm:"TEXT"`
	CreatedUnix timeutil.TimeStamp `xorm:"created NOT NULL"`
	UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
}

func init() {
	db.RegisterModel(new(EnvironmentVariable))
}

// InsertEnvironmentVariable creates an environment-scoped variable.
func InsertEnvironmentVariable(ctx context.Context, repoID, envID int64, name, data, description string) (*EnvironmentVariable, error) {
	if utf8.RuneCountInString(data) > VariableDataMaxLength {
		return nil, util.NewInvalidArgumentErrorf("data too long")
	}
	description = util.TruncateRunes(description, VariableDescriptionMaxLength)

	variable := &EnvironmentVariable{
		RepoID:      repoID,
		EnvID:       envID,
		Name:        strings.ToUpper(name),
		Data:        data,
		Description: description,
	}
	return variable, db.Insert(ctx, variable)
}

type FindEnvironmentVariablesOpts struct {
	db.ListOptions
	RepoID int64
	EnvID  int64
	Name   string
}

func (opts FindEnvironmentVariablesOpts) ToConds() builder.Cond {
	cond := builder.NewCond()
	if opts.RepoID > 0 {
		cond = cond.And(builder.Eq{"repo_id": opts.RepoID})
	}
	if opts.EnvID > 0 {
		cond = cond.And(builder.Eq{"env_id": opts.EnvID})
	}
	if opts.Name != "" {
		cond = cond.And(builder.Eq{"name": strings.ToUpper(opts.Name)})
	}
	return cond
}

func (opts FindEnvironmentVariablesOpts) ToOrders() string {
	return "name"
}

// FindEnvironmentVariables lists environment-scoped variables.
func FindEnvironmentVariables(ctx context.Context, opts FindEnvironmentVariablesOpts) ([]*EnvironmentVariable, error) {
	return db.Find[EnvironmentVariable](ctx, opts)
}

// UpdateEnvironmentVariableCols persists the given columns of an environment variable.
func UpdateEnvironmentVariableCols(ctx context.Context, variable *EnvironmentVariable, cols ...string) (bool, error) {
	if utf8.RuneCountInString(variable.Data) > VariableDataMaxLength {
		return false, util.NewInvalidArgumentErrorf("data too long")
	}
	variable.Description = util.TruncateRunes(variable.Description, VariableDescriptionMaxLength)
	variable.Name = strings.ToUpper(variable.Name)
	count, err := db.GetEngine(ctx).
		ID(variable.ID).
		Cols(cols...).
		Update(variable)
	return count != 0, err
}

// DeleteEnvironmentVariable deletes an environment variable by ID.
func DeleteEnvironmentVariable(ctx context.Context, id int64) error {
	_, err := db.DeleteByID[EnvironmentVariable](ctx, id)
	return err
}

// GetEnvironmentVariablesMap returns the variables of one environment as a name->data map.
func GetEnvironmentVariablesMap(ctx context.Context, envID int64) (map[string]string, error) {
	variables, err := FindEnvironmentVariables(ctx, FindEnvironmentVariablesOpts{EnvID: envID})
	if err != nil {
		return nil, err
	}
	ret := make(map[string]string, len(variables))
	for _, v := range variables {
		ret[v.Name] = v.Data
	}
	return ret, nil
}
