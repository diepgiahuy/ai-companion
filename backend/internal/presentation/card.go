package presentation

import (
	"fmt"
	"unicode"
	"unicode/utf8"
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
	if len(c.Kind) == 0 || len(c.Kind) > maximumKindBytes || !validKind(c.Kind) {
		return fmt.Errorf("card kind must be a 1..%d byte ASCII token", maximumKindBytes)
	}
	if err := validateText("card title", c.Title, maximumTitleBytes); err != nil {
		return err
	}
	if err := validateText("card primary", c.Primary, maximumTextBytes); err != nil {
		return err
	}
	if err := validateText("card secondary", c.Secondary, maximumTextBytes); err != nil {
		return err
	}
	if c.Progress < 0 || c.Progress > 100 {
		return fmt.Errorf("card progress must be 0..100")
	}
	return nil
}

func validKind(kind string) bool {
	for _, ch := range kind {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '.' {
			continue
		}
		return false
	}
	return true
}

func validateText(name, value string, maximumBytes int) error {
	if len(value) > maximumBytes {
		return fmt.Errorf("%s exceeds %d bytes", name, maximumBytes)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return nil
}
