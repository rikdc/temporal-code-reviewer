package metrics

import (
	"context"
	"math/rand"
)

// PromptRegistry selects prompt versions from the database with equal
// distribution among all active (non-disabled) versions. When no database
// versions exist it falls back to the on-disk loader.
//
// It implements llm.PromptSource.
type PromptRegistry struct {
	repo     Repository
	fallback diskLoader
}

// diskLoader is the minimal interface we need from llm.PromptLoader to avoid
// an import cycle (metrics → llm would create a cycle since llm imports nothing
// from metrics, but if llm imported metrics it would cycle).
type diskLoader interface {
	Load(filename string) (string, error)
}

// NewPromptRegistry creates a registry backed by repo, falling back to loader
// for agents with no database versions.
func NewPromptRegistry(repo Repository, loader diskLoader) *PromptRegistry {
	return &PromptRegistry{repo: repo, fallback: loader}
}

// LoadForAgent selects an active prompt version at random (equal weight).
// If the agent has no active versions in the database it falls back to the
// on-disk file identified by fallbackFile. The returned versionID is the UUID
// of the chosen DB row, or "" for the disk fallback.
func (r *PromptRegistry) LoadForAgent(ctx context.Context, agentName, fallbackFile string) (content, versionID string, err error) {
	versions, err := r.repo.GetActivePromptVersions(ctx, agentName)
	if err != nil || len(versions) == 0 {
		content, err = r.fallback.Load(fallbackFile)
		return content, "", err
	}
	chosen := versions[rand.Intn(len(versions))]
	return chosen.Content, chosen.ID, nil
}
