// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package swagger

import api "gitea.dev/modules/structs"

// Request body models referenced by the environment API endpoints. Kept in this package so the
// spec generator scans the request structs (swagger:model) just like it does for response bodies.
//
// swagger:response CreateOrUpdateEnvironmentOption
type _swaggerCreateOrUpdateEnvironmentOption struct {
	// in:body
	Body api.CreateOrUpdateEnvironmentOption `json:"body"`
}

// swagger:response LockEnvironmentOption
type _swaggerLockEnvironmentOption struct {
	// in:body
	Body api.LockEnvironmentOption `json:"body"`
}

// swagger:response CreateFreezeWindowOption
type _swaggerCreateFreezeWindowOption struct {
	// in:body
	Body api.CreateFreezeWindowOption `json:"body"`
}

// swagger:response CreateEnvironmentVariableOption
type _swaggerCreateEnvironmentVariableOption struct {
	// in:body
	Body api.CreateEnvironmentVariableOption `json:"body"`
}

// swagger:response UpdateEnvironmentVariableOption
type _swaggerUpdateEnvironmentVariableOption struct {
	// in:body
	Body api.UpdateEnvironmentVariableOption `json:"body"`
}

// swagger:response CreateDeploymentReviewOption
type _swaggerCreateDeploymentReviewOption struct {
	// in:body
	Body api.CreateDeploymentReviewOption `json:"body"`
}
