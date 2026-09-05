// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package swagger

import api "gitea.dev/modules/structs"

// SecretList
// swagger:response SecretList
type swaggerResponseSecretList struct {
	// in:body
	Body []api.Secret `json:"body"`
}

// ActionVariable
// swagger:response ActionVariable
type swaggerResponseActionVariable struct {
	// in:body
	Body api.ActionVariable `json:"body"`
}

// VariableList
// swagger:response VariableList
type swaggerResponseVariableList struct {
	// in:body
	Body []api.ActionVariable `json:"body"`
}

// ActionWorkflow
// swagger:response ActionWorkflow
type swaggerResponseActionWorkflow struct {
	// in:body
	Body api.ActionWorkflow `json:"body"`
}

// ActionWorkflowList
// swagger:response ActionWorkflowList
type swaggerResponseActionWorkflowList struct {
	// in:body
	Body api.ActionWorkflowResponse `json:"body"`
}

// RunDetails
// swagger:response RunDetails
type swaggerResponseRunDetails struct {
	// in:body
	Body api.RunDetails `json:"body"`
}

// Environment
// swagger:response Environment
type swaggerResponseEnvironment struct {
	// in:body
	Body api.Environment `json:"body"`
}

// EnvironmentList
// swagger:response EnvironmentList
type swaggerResponseEnvironmentList struct {
	// in:body
	Body []api.Environment `json:"body"`
}

// FreezeWindow
// swagger:response FreezeWindow
type swaggerResponseFreezeWindow struct {
	// in:body
	Body api.FreezeWindow `json:"body"`
}

// FreezeWindowList
// swagger:response FreezeWindowList
type swaggerResponseFreezeWindowList struct {
	// in:body
	Body []api.FreezeWindow `json:"body"`
}

// EnvironmentVariable
// swagger:response EnvironmentVariable
type swaggerResponseEnvironmentVariable struct {
	// in:body
	Body api.EnvironmentVariable `json:"body"`
}

// EnvironmentVariableList
// swagger:response EnvironmentVariableList
type swaggerResponseEnvironmentVariableList struct {
	// in:body
	Body []api.EnvironmentVariable `json:"body"`
}

// EnvironmentSecretList
// swagger:response EnvironmentSecretList
type swaggerResponseEnvironmentSecretList struct {
	// in:body
	Body []api.EnvironmentSecret `json:"body"`
}

// Deployment
// swagger:response Deployment
type swaggerResponseDeployment struct {
	// in:body
	Body api.Deployment `json:"body"`
}

// DeploymentList
// swagger:response DeploymentList
type swaggerResponseDeploymentList struct {
	// in:body
	Body []api.Deployment `json:"body"`
}
