// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hook

import (
	"errors"
	"fmt"
	"regexp"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/leader"
)

// keyPattern keeps election keys usable as one koanf path segment and one
// env var segment (APP_LEADER_ELECTIONS_<KEY>_*), which splits on "_".
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,62}$`)

// ErrInvalidKey reports a named-election key outside [a-z][a-z0-9]{0,62}.
var ErrInvalidKey = errors.New("invalid election key")

// ConfigPath is the koanf path holding the options of the election key.
func ConfigPath(key string) string { return "leader.elections." + key }

// NamedStatus validates key and returns a provider of a *leader.Status
// tagged name:"<key>", plus that tag for the backend's own fx.ParamTags.
func NamedStatus(key string) (fx.Option, string, error) {
	if !keyPattern.MatchString(key) {
		return nil, "", fmt.Errorf("%w %q: want [a-z][a-z0-9]{0,62}", ErrInvalidKey, key)
	}
	tag := `name:"` + key + `"`
	return fx.Provide(fx.Annotate(leader.NewStatus, fx.ResultTags(tag))), tag, nil
}
