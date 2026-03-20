package activities

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/rikdc/temporal-code-reviewer/config"
	"github.com/rikdc/temporal-code-reviewer/llm"
	"github.com/rikdc/temporal-code-reviewer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.uber.org/zap"
)

// mockPublisher implements events.Publisher for testing.
type mockPublisher struct {
	mock.Mock
}

func (m *mockPublisher) Publish(event types.WorkflowEvent) {
	m.Called(event)
}

// mockReviewer implements llm.Reviewer for testing.
type mockReviewer struct {
	mock.Mock
}

func (m *mockReviewer) Review(ctx context.Context, req llm.ReviewRequest) (*llm.ReviewResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llm.ReviewResponse), args.Error(1)
}

func newTestActivityEnv(t *testing.T) *testsuite.TestActivityEnvironment {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	return suite.NewTestActivityEnvironment()
}

func TestReviewAgent_Execute_Success(t *testing.T) {
	pub := new(mockPublisher)
	pub.On("Publish", mock.AnythingOfType("types.WorkflowEvent")).Return()

	reviewer := new(mockReviewer)
	reviewer.On("Review", mock.Anything, mock.AnythingOfType("llm.ReviewRequest")).Return(&llm.ReviewResponse{
		Content:      `{"status":"passed","findings":[],"summary":"No issues found"}`,
		Model:        "test-model",
		InputTokens:  100,
		OutputTokens: 50,
	}, nil)

	promptDir := t.TempDir()
	require.NoError(t, writePromptFile(promptDir, "security.txt", "You are a security reviewer."))

	agent := &ReviewAgent{
		Name:         "Security",
		ReviewFocus:  "security vulnerabilities",
		EventBus:     pub,
		Logger:       zap.NewNop(),
		LLMClient:    reviewer,
		Config:       &config.AgentConfig{Model: "test-model", MaxTokens: 1000, Temperature: 0.3, PromptFile: "security.txt"},
		PromptLoader: llm.NewPromptLoader(promptDir),
	}

	env := newTestActivityEnv(t)
	env.RegisterActivity(agent.Execute)

	val, err := env.ExecuteActivity(agent.Execute, types.AgentReviewInput{
		PRReviewInput: types.PRReviewInput{PRNumber: 42, Title: "Test PR", RepoOwner: "rikdc", RepoName: "service"},
		DiffContent:   "+added line",
	})

	require.NoError(t, err)

	var result types.AgentResult
	require.NoError(t, val.Get(&result))
	assert.Equal(t, "Security", result.AgentName)
	assert.Equal(t, types.StatusPassed, result.Status)
	assert.Equal(t, 100, result.Progress)

	reviewer.AssertCalled(t, "Review", mock.Anything, mock.AnythingOfType("llm.ReviewRequest"))
	pub.AssertCalled(t, "Publish", mock.AnythingOfType("types.WorkflowEvent"))
}

func TestReviewAgent_Execute_LLMError(t *testing.T) {
	pub := new(mockPublisher)
	pub.On("Publish", mock.AnythingOfType("types.WorkflowEvent")).Return()

	reviewer := new(mockReviewer)
	reviewer.On("Review", mock.Anything, mock.AnythingOfType("llm.ReviewRequest")).
		Return(nil, errors.New("LLM unavailable"))

	promptDir := t.TempDir()
	require.NoError(t, writePromptFile(promptDir, "security.txt", "prompt"))

	agent := &ReviewAgent{
		Name:         "Security",
		ReviewFocus:  "security vulnerabilities",
		EventBus:     pub,
		Logger:       zap.NewNop(),
		LLMClient:    reviewer,
		Config:       &config.AgentConfig{Model: "test-model", MaxTokens: 1000, Temperature: 0.3, PromptFile: "security.txt"},
		PromptLoader: llm.NewPromptLoader(promptDir),
	}

	env := newTestActivityEnv(t)
	env.RegisterActivity(agent.Execute)

	_, err := env.ExecuteActivity(agent.Execute, types.AgentReviewInput{
		PRReviewInput: types.PRReviewInput{PRNumber: 42, Title: "Test PR", RepoOwner: "rikdc", RepoName: "service"},
		DiffContent:   "+added line",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "LLM unavailable")
}

func TestReviewAgent_Execute_InvalidJSON(t *testing.T) {
	pub := new(mockPublisher)
	pub.On("Publish", mock.AnythingOfType("types.WorkflowEvent")).Return()

	reviewer := new(mockReviewer)
	reviewer.On("Review", mock.Anything, mock.AnythingOfType("llm.ReviewRequest")).Return(&llm.ReviewResponse{
		Content:      "not valid json at all",
		Model:        "test-model",
		InputTokens:  50,
		OutputTokens: 30,
	}, nil)

	promptDir := t.TempDir()
	require.NoError(t, writePromptFile(promptDir, "security.txt", "prompt"))

	agent := &ReviewAgent{
		Name:         "Security",
		ReviewFocus:  "security vulnerabilities",
		EventBus:     pub,
		Logger:       zap.NewNop(),
		LLMClient:    reviewer,
		Config:       &config.AgentConfig{Model: "test-model", MaxTokens: 1000, Temperature: 0.3, PromptFile: "security.txt"},
		PromptLoader: llm.NewPromptLoader(promptDir),
	}

	env := newTestActivityEnv(t)
	env.RegisterActivity(agent.Execute)

	val, err := env.ExecuteActivity(agent.Execute, types.AgentReviewInput{
		PRReviewInput: types.PRReviewInput{PRNumber: 42, Title: "Test PR", RepoOwner: "rikdc", RepoName: "service"},
		DiffContent:   "+added line",
	})

	require.NoError(t, err)

	var result types.AgentResult
	require.NoError(t, val.Get(&result))
	assert.Equal(t, types.StatusWarning, result.Status)
	assert.NotEmpty(t, result.Findings)
}

func TestNewSecurityAgent_AcceptsInterfaces(t *testing.T) {
	pub := new(mockPublisher)
	reviewer := new(mockReviewer)

	agent := NewSecurityAgent(pub, zap.NewNop(), reviewer, &config.AgentConfig{
		Model: "m", MaxTokens: 1, Temperature: 0, PromptFile: "f",
	}, llm.NewPromptLoader("."))

	assert.Equal(t, "Security", agent.Name)
	assert.Equal(t, pub, agent.EventBus)
	assert.Equal(t, reviewer, agent.LLMClient)
}

func writePromptFile(dir, name, content string) error {
	return os.WriteFile(dir+"/"+name, []byte(content), 0644)
}
