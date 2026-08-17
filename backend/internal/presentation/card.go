package presentation

import (
	"fmt"
	"strings"
)

const (
	CardVersion       = 1
	maximumKindBytes  = 32
	maximumTitleBytes = 96
	maximumTextBytes  = 192
)

// CardV1 is the bounded semantic card carried from the agent boundary to the
// device presentation ingress. It is data only: no markup, URL, script, remote
// asset fetch, priority, or executable action can be expressed by this schema.
type CardV1 struct {
	Version   int    `json:"version"`
	Kind      string `json:"kind"`
	Title     string `json:"title,omitempty"`
	Primary   string `json:"primary,omitempty"`
	Secondary string `json:"secondary,omitempty"`
	Progress  int    `json:"progress,omitempty"`
}

func NewCardV1(kind, title, primary, secondary string, progress int) (CardV1, error) {
	card := CardV1{
		Version:   CardVersion,
		Kind:      kind,
		Title:     title,
		Primary:   primary,
		Secondary: secondary,
		Progress:  progress,
	}
	if err := card.Validate(); err != nil {
		return CardV1{}, err
	}
	return card, nil
}

func (c CardV1) Validate() error {
	if c.Version != CardVersion {
		return fmt.Errorf("card version must be %d", CardVersion)
	}
	if strings.TrimSpace(c.Kind) == "" || len(c.Kind) > maximumKindBytes {
		return fmt.Errorf("card kind must be 1..%d bytes", maximumKindBytes)
	}
	if len(c.Title) > maximumTitleBytes {
		return fmt.Errorf("card title exceeds %d bytes", maximumTitleBytes)
	}
	if len(c.Primary) > maximumTextBytes {
		return fmt.Errorf("card primary exceeds %d bytes", maximumTextBytes)
	}
	if len(c.Secondary) > maximumTextBytes {
		return fmt.Errorf("card secondary exceeds %d bytes", maximumTextBytes)
	}
	if c.Progress < 0 || c.Progress > 100 {
		return fmt.Errorf("card progress must be 0..100")
	}
	return nil
}
