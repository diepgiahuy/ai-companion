package controlplane

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// FeatureModule describes a product capability independently from rollout,
// entitlement and implementation details. The manifest is control-plane data;
// it never causes arbitrary code to be loaded into the server process.
type FeatureModule struct {
	ID             string   `json:"id"`
	Version        int      `json:"version"`
	Lifecycle      string   `json:"lifecycle"`
	Execution      string   `json:"execution"` // native | external
	MinProtocol    int      `json:"min_protocol,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
	Tools          []string `json:"tools,omitempty"`
	Resources      []string `json:"resources,omitempty"`
	UICards        []string `json:"ui_cards,omitempty"`
	Locales        []string `json:"locales,omitempty"`
	ConfigKeys     []string `json:"config_keys,omitempty"`
	Entitlement    string   `json:"entitlement,omitempty"`
	Implementation string   `json:"implementation,omitempty"` // adapter key, never executable code/URL
}

type FeatureCatalogRepository interface {
	PutFeatureModule(context.Context, FeatureModule) error
	FeatureModules(context.Context) ([]FeatureModule, error)
	FeatureModule(context.Context, string) (FeatureModule, bool, error)
}

type FeatureCatalog struct{ repo FeatureCatalogRepository }

func NewFeatureCatalog(repo FeatureCatalogRepository) *FeatureCatalog {
	return &FeatureCatalog{repo: repo}
}
func (c *FeatureCatalog) Put(ctx context.Context, m FeatureModule) error {
	if err := ValidateFeatureModule(m); err != nil {
		return err
	}
	if old, ok, err := c.repo.FeatureModule(ctx, m.ID); err != nil {
		return err
	} else if ok && m.Version < old.Version {
		return fmt.Errorf("feature version rollback rejected")
	}
	return c.repo.PutFeatureModule(ctx, m)
}
func (c *FeatureCatalog) List(ctx context.Context) ([]FeatureModule, error) {
	xs, e := c.repo.FeatureModules(ctx)
	sort.Slice(xs, func(i, j int) bool { return xs[i].ID < xs[j].ID })
	return xs, e
}
func ValidateFeatureModule(m FeatureModule) error {
	if strings.TrimSpace(m.ID) == "" || m.Version <= 0 {
		return fmt.Errorf("feature id and positive version required")
	}
	switch m.Lifecycle {
	case "draft", "internal", "beta", "released", "deprecated", "removed":
	default:
		return fmt.Errorf("invalid feature lifecycle")
	}
	if m.Execution != "native" && m.Execution != "external" {
		return fmt.Errorf("execution must be native or external")
	}
	if m.MinProtocol < 0 {
		return fmt.Errorf("min_protocol cannot be negative")
	}
	return nil
}
