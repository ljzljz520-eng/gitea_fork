// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package jobparser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseJob(t *testing.T, wf string) *Job {
	t.Helper()
	sws, err := Parse([]byte(wf))
	require.NoError(t, err)
	require.Len(t, sws, 1)
	_, job := sws[0].Job()
	require.NotNil(t, job)
	return job
}

func TestEnvironmentSpec(t *testing.T) {
	t.Run("scalar string", func(t *testing.T) {
		job := parseJob(t, `name: demo
on: push
jobs:
  deploy:
    runs-on: ubuntu-latest
    environment: production
    steps:
      - run: echo
`)
		name, url, ok, err := job.EnvironmentSpec()
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "production", name)
		assert.Empty(t, url)
		assert.True(t, job.HasEnvironment())
	})

	t.Run("mapping with url", func(t *testing.T) {
		job := parseJob(t, `name: demo
on: push
jobs:
  deploy:
    runs-on: ubuntu-latest
    environment:
      name: staging
      url: https://staging.example.com
    steps:
      - run: echo
`)
		name, url, ok, err := job.EnvironmentSpec()
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "staging", name)
		assert.Equal(t, "https://staging.example.com", url)
	})

	t.Run("mapping without name is invalid", func(t *testing.T) {
		job := parseJob(t, `name: demo
on: push
jobs:
  deploy:
    runs-on: ubuntu-latest
    environment:
      url: https://example.com
    steps:
      - run: echo
`)
		_, _, ok, err := job.EnvironmentSpec()
		require.Error(t, err)
		assert.False(t, ok)
	})

	t.Run("no environment", func(t *testing.T) {
		job := parseJob(t, `name: demo
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo
`)
		_, _, ok, err := job.EnvironmentSpec()
		require.NoError(t, err)
		assert.False(t, ok)
		assert.False(t, job.HasEnvironment())
	})
}

func TestEvaluateEnvironment(t *testing.T) {
	base := func(envYaml string) *Job {
		return parseJob(t, `name: demo
on: push
jobs:
  deploy:
    runs-on: ubuntu-latest
    environment:
`+envYaml+`
    steps:
      - run: echo
`)
	}

	t.Run("static name", func(t *testing.T) {
		job := parseJob(t, `name: demo
on: push
jobs:
  deploy:
    runs-on: ubuntu-latest
    environment: production
    steps:
      - run: echo
`)
		name, url, err := EvaluateEnvironment("deploy", job, map[string]any{}, callerResults("deploy", nil, nil), nil, nil)
		require.NoError(t, err)
		assert.Equal(t, "production", name)
		assert.Empty(t, url)
	})

	t.Run("name from inputs", func(t *testing.T) {
		job := base("      name: ${{ inputs.env_name }}\n      url: https://${{ inputs.env_name }}.example.com")
		name, url, err := EvaluateEnvironment("deploy", job, map[string]any{}, callerResults("deploy", nil, nil), nil, map[string]any{"env_name": "qa"})
		require.NoError(t, err)
		assert.Equal(t, "qa", name)
		assert.Equal(t, "https://qa.example.com", url)
	})

	t.Run("name from matrix", func(t *testing.T) {
		sws, err := Parse([]byte(`name: demo
on: push
jobs:
  deploy:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        env: [prod, dev]
    environment: ${{ matrix.env }}
    steps:
      - run: echo
`))
		require.NoError(t, err)
		require.Len(t, sws, 2) // one single-workflow per matrix combination
		_, job := sws[0].Job()
		name, _, err := EvaluateEnvironment("deploy", job, map[string]any{}, callerResults("deploy", nil, nil), nil, nil)
		require.NoError(t, err)
		assert.Contains(t, []string{"prod", "dev"}, name)
	})

	t.Run("no environment evaluates empty", func(t *testing.T) {
		job := parseJob(t, `name: demo
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo
`)
		name, url, err := EvaluateEnvironment("build", job, map[string]any{}, callerResults("build", nil, nil), nil, nil)
		require.NoError(t, err)
		assert.Empty(t, name)
		assert.Empty(t, url)
	})
}

// TestEnvironmentPreservedInPayload ensures the environment key survives
// SingleWorkflow.SetJob + Marshal round-trip (the server re-serializes jobs before persisting them).
func TestEnvironmentPreservedInPayload(t *testing.T) {
	job := parseJob(t, `name: demo
on: push
jobs:
  deploy:
    runs-on: ubuntu-latest
    environment:
      name: production
      url: https://prod.example.com
    steps:
      - run: echo
`)

	sw := &SingleWorkflow{Name: "demo"}
	require.NoError(t, sw.SetJob("deploy", job))
	payload, err := sw.Marshal()
	require.NoError(t, err)

	_, got, err := ParseRawSingleWorkflow(payload)
	require.NoError(t, err)
	name, url, ok, err := got.EnvironmentSpec()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "production", name)
	assert.Equal(t, "https://prod.example.com", url)
}
