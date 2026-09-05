// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"strings"

	"gitea.dev/models/db"
	"gitea.dev/modules/log"
	secret_module "gitea.dev/modules/secret"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

// EnvironmentSecret is an Actions secret scoped to a repository environment. Data is encrypted at rest.
type EnvironmentSecret struct {
	ID          int64              `xorm:"pk autoincr"`
	RepoID      int64              `xorm:"INDEX NOT NULL"`
	EnvID       int64              `xorm:"UNIQUE(env_name) INDEX NOT NULL"`
	Name        string             `xorm:"UNIQUE(env_name) NOT NULL"`
	Data        string             `xorm:"LONGTEXT"` // encrypted data
	Description string             `xorm:"TEXT"`
	CreatedUnix timeutil.TimeStamp `xorm:"created NOT NULL"`
}

// mirrors models/secret limits; duplicated to avoid the import cycle with models/secret
const (
	envSecretDataMaxLength        = 65536
	envSecretDescriptionMaxLength = 4096
)

func init() {
	db.RegisterModel(new(EnvironmentSecret))
}

// InsertEncryptedEnvironmentSecret creates an environment secret from plaintext data.
func InsertEncryptedEnvironmentSecret(ctx context.Context, repoID, envID int64, name, data, description string) (*EnvironmentSecret, error) {
	if len(data) > envSecretDataMaxLength {
		return nil, util.NewInvalidArgumentErrorf("data too long")
	}
	description = util.TruncateRunes(description, envSecretDescriptionMaxLength)

	encrypted, err := secret_module.EncryptSecret(setting.SecretKey, data)
	if err != nil {
		return nil, err
	}

	secret := &EnvironmentSecret{
		RepoID:      repoID,
		EnvID:       envID,
		Name:        strings.ToUpper(name),
		Data:        encrypted,
		Description: description,
	}
	return secret, db.Insert(ctx, secret)
}

type FindEnvironmentSecretsOpts struct {
	db.ListOptions
	RepoID int64
	EnvID  int64
	Name   string
}

func (opts FindEnvironmentSecretsOpts) ToConds() builder.Cond {
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

func (opts FindEnvironmentSecretsOpts) ToOrders() string {
	return "name"
}

// FindEnvironmentSecrets lists environment-scoped secrets (still encrypted).
func FindEnvironmentSecrets(ctx context.Context, opts FindEnvironmentSecretsOpts) ([]*EnvironmentSecret, error) {
	return db.Find[EnvironmentSecret](ctx, opts)
}

// UpdateEnvironmentSecret updates the (plaintext) data and description of an environment secret.
func UpdateEnvironmentSecret(ctx context.Context, secretID int64, data, description string) error {
	if len(data) > envSecretDataMaxLength {
		return util.NewInvalidArgumentErrorf("data too long")
	}
	description = util.TruncateRunes(description, envSecretDescriptionMaxLength)

	encrypted, err := secret_module.EncryptSecret(setting.SecretKey, data)
	if err != nil {
		return err
	}

	s := &EnvironmentSecret{
		Data:        encrypted,
		Description: description,
	}
	affected, err := db.GetEngine(ctx).ID(secretID).Cols("data", "description").Update(s)
	if affected != 1 {
		return util.ErrNotExist
	}
	return err
}

// DeleteEnvironmentSecret deletes an environment secret by ID.
func DeleteEnvironmentSecret(ctx context.Context, id int64) error {
	_, err := db.DeleteByID[EnvironmentSecret](ctx, id)
	return err
}

// GetEnvironmentSecretsMap returns decrypted secrets of one environment as a name->value map.
// Secrets that cannot be decrypted (e.g. wrong SECRET_KEY) are skipped and logged.
func GetEnvironmentSecretsMap(ctx context.Context, envID int64) (map[string]string, error) {
	secrets, err := FindEnvironmentSecrets(ctx, FindEnvironmentSecretsOpts{EnvID: envID})
	if err != nil {
		return nil, err
	}
	ret := make(map[string]string, len(secrets))
	for _, secret := range secrets {
		v, err := secret_module.DecryptSecret(setting.SecretKey, secret.Data)
		if err != nil {
			log.Error("Unable to decrypt environment secret %v %q, maybe SECRET_KEY is wrong: %v", secret.ID, secret.Name, err)
			continue
		}
		ret[secret.Name] = v
	}
	return ret, nil
}
