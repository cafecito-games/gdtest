// Package dynamic caches engine-completed discovery manifests by semantic
// content identity without embedding live worktree paths in reusable keys.
package dynamic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/cafecito-games/gdtest/internal/cache"
	"github.com/cafecito-games/gdtest/internal/model"
)

const schemaVersion = "dynamic-manifest-v1"

var ErrInvalidManifest = errors.New("invalid dynamic discovery manifest")

// Identity contains every semantic input to engine-dependent discovery.
type Identity struct {
	ToolVersion        string
	RuntimeVersion     string
	GodotIdentity      string
	Configuration      []byte
	ContentFingerprint string
	Generation         uint64
}

// Key derives a worktree-neutral cache key.
func Key(identity Identity) (string, error) {
	if identity.ToolVersion == "" || identity.RuntimeVersion == "" ||
		identity.GodotIdentity == "" || identity.ContentFingerprint == "" ||
		identity.Generation == 0 {
		return "", errors.New("dynamic cache identity is incomplete")
	}
	engine := strings.Join([]string{
		schemaVersion, identity.RuntimeVersion, identity.GodotIdentity,
	}, "\x00")
	return cache.Key(
		identity.ToolVersion,
		engine,
		identity.Configuration,
		[]byte(identity.ContentFingerprint),
	), nil
}

// LoadOrCompute returns a verified cached manifest or publishes one computed
// by a compatible Godot worker. The bool reports a cache hit.
func LoadOrCompute(
	ctx context.Context,
	store *cache.Store,
	identity Identity,
	compute func(context.Context) (model.DiscoveryManifest, error),
) (model.DiscoveryManifest, bool, error) {
	if store == nil {
		return model.DiscoveryManifest{}, false, errors.New("dynamic cache store is nil")
	}
	key, err := Key(identity)
	if err != nil {
		return model.DiscoveryManifest{}, false, err
	}
	if data, hit, getErr := store.Get(ctx, key); getErr != nil {
		return model.DiscoveryManifest{}, false, getErr
	} else if hit {
		manifest, decodeErr := decode(data, identity.ContentFingerprint)
		if decodeErr == nil {
			// Generation is live session metadata, not semantic cache identity.
			manifest.Generation = identity.Generation
			return manifest, true, nil
		}
		// The generic cache envelope is intact, but its application payload is
		// unusable. Refuse the value and remove it before the coalesced rebuild.
		if deleteErr := store.Delete(ctx, key); deleteErr != nil {
			return model.DiscoveryManifest{}, false, errors.Join(decodeErr, deleteErr)
		}
	}
	if compute == nil {
		return model.DiscoveryManifest{}, false, errors.New("dynamic manifest compute function is nil")
	}
	data, err := store.GetOrCompute(ctx, key, func(computeCtx context.Context) ([]byte, error) {
		manifest, computeErr := compute(computeCtx)
		if computeErr != nil {
			return nil, computeErr
		}
		if validateErr := validate(manifest, identity.ContentFingerprint); validateErr != nil {
			return nil, validateErr
		}
		if manifest.Generation != identity.Generation {
			return nil, fmt.Errorf(
				"%w: computed generation %d does not match %d",
				ErrInvalidManifest,
				manifest.Generation,
				identity.Generation,
			)
		}
		return json.Marshal(manifest)
	})
	if err != nil {
		return model.DiscoveryManifest{}, false, err
	}
	manifest, err := decode(data, identity.ContentFingerprint)
	manifest.Generation = identity.Generation
	return manifest, false, err
}

func decode(data []byte, fingerprint string) (model.DiscoveryManifest, error) {
	var manifest model.DiscoveryManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return model.DiscoveryManifest{}, fmt.Errorf("%w: decode: %v", ErrInvalidManifest, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return model.DiscoveryManifest{}, fmt.Errorf("%w: trailing content", ErrInvalidManifest)
	}
	if err := validate(manifest, fingerprint); err != nil {
		return model.DiscoveryManifest{}, err
	}
	return manifest, nil
}

func validate(manifest model.DiscoveryManifest, fingerprint string) error {
	if manifest.ProtocolVersion != model.ProtocolVersion || manifest.Generation == 0 ||
		manifest.ContentFingerprint != fingerprint {
		return fmt.Errorf("%w: protocol, generation, or fingerprint mismatch", ErrInvalidManifest)
	}
	seen := make(map[string]struct{})
	for _, file := range manifest.Files {
		if !validManifestPath(file.Path) {
			return fmt.Errorf("%w: unsafe file path %q", ErrInvalidManifest, file.Path)
		}
		for _, testCase := range file.Cases {
			if testCase.ID == "" {
				return fmt.Errorf("%w: empty case ID", ErrInvalidManifest)
			}
			idFile, _, hasSeparator := strings.Cut(testCase.ID, "::")
			if !hasSeparator || idFile != file.Path || !validManifestPath(testCase.File) {
				return fmt.Errorf("%w: invalid case identity %q", ErrInvalidManifest, testCase.ID)
			}
			if _, exists := seen[testCase.ID]; exists {
				return fmt.Errorf("%w: duplicate case ID %q", ErrInvalidManifest, testCase.ID)
			}
			seen[testCase.ID] = struct{}{}
			for _, suite := range testCase.Suites {
				if suite.ID == "" || !validManifestPath(suite.File) {
					return fmt.Errorf("%w: invalid suite metadata for %q", ErrInvalidManifest, testCase.ID)
				}
			}
		}
	}
	for _, discoveryError := range manifest.Errors {
		if discoveryError.Location != nil && !validManifestPath(discoveryError.Location.File) {
			return fmt.Errorf("%w: unsafe diagnostic path %q", ErrInvalidManifest, discoveryError.Location.File)
		}
	}
	return nil
}

func validManifestPath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, `\`) ||
		strings.HasPrefix(value, "res://") || strings.HasPrefix(value, "/") ||
		(len(value) >= 3 && value[1] == ':' && value[2] == '/') {
		return false
	}
	cleanPath := filepath.ToSlash(filepath.Clean(value))
	return cleanPath == value && cleanPath != "." && cleanPath != ".." &&
		!strings.HasPrefix(cleanPath, "../")
}
