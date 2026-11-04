// Package index composes content-addressed static discovery without embedding
// worktree paths in reusable cache identities.
package index

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/cafecito-games/gdtest/internal/cache"
	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/generation"
	"github.com/cafecito-games/gdtest/internal/version"
	sourcewatch "github.com/cafecito-games/gdtest/internal/watch"
)

const gdparserModule = "github.com/cafecito-games/gdparser"

// Discover returns a verified manifest, reusing an analysis artifact when all
// source and configuration inputs have the same content identity.
func Discover(ctx context.Context, root string, options discovery.Options) (discovery.Manifest, error) {
	store, err := cache.Default(version.BuildID())
	if err != nil {
		return discovery.Discover(root, options)
	}
	return discoverWithStore(ctx, root, options, store)
}

func discoverWithStore(
	ctx context.Context,
	root string,
	options discovery.Options,
	store *cache.Store,
) (discovery.Manifest, error) {
	paths, err := sourcewatch.RelevantPaths(ctx, root)
	if err != nil {
		return discovery.Manifest{}, err
	}
	sources := paths[:0]
	for _, name := range paths {
		if strings.EqualFold(filepath.Ext(name), ".gd") {
			sources = append(sources, name)
		}
	}
	sort.Strings(sources)
	snapshot, err := generation.Build(ctx, root, sources)
	if err != nil {
		return discovery.Manifest{}, fmt.Errorf("fingerprint discovery inputs: %w", err)
	}
	configuration, err := json.Marshal(options)
	if err != nil {
		return discovery.Manifest{}, fmt.Errorf("encode discovery configuration: %w", err)
	}
	content, err := json.Marshal(snapshot)
	if err != nil {
		return discovery.Manifest{}, fmt.Errorf("encode discovery content identity: %w", err)
	}
	key := cache.Key(version.BuildID(), parserVersion(), configuration, content)
	encoded, err := store.GetOrCompute(ctx, key, func(context.Context) ([]byte, error) {
		manifest, discoverErr := discovery.Discover(root, options)
		if discoverErr != nil {
			return nil, discoverErr
		}
		return json.Marshal(manifest)
	})
	if err != nil {
		return discovery.Manifest{}, err
	}
	manifest, err := decodeManifest(encoded)
	if err != nil {
		// The cache envelope may be intact while its application payload is not.
		// Recompute rather than trusting or exposing a malformed analysis result.
		manifest, discoverErr := discovery.Discover(root, options)
		if discoverErr != nil {
			return discovery.Manifest{}, discoverErr
		}
		fresh, marshalErr := json.Marshal(manifest)
		if marshalErr == nil {
			_ = store.Put(ctx, key, fresh)
		}
		return manifest, nil
	}
	return manifest, nil
}

func decodeManifest(data []byte) (discovery.Manifest, error) {
	var manifest discovery.Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return discovery.Manifest{}, fmt.Errorf("decode cached discovery manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return discovery.Manifest{}, errors.New("decode cached discovery manifest: trailing content")
	} else if !errors.Is(err, io.EOF) {
		return discovery.Manifest{}, fmt.Errorf("decode cached discovery manifest: trailing content: %w", err)
	}
	if manifest.ContentFingerprint == "" || manifest.StructuralFingerprint == "" {
		return discovery.Manifest{}, fmt.Errorf("decode cached discovery manifest: missing fingerprints")
	}
	return manifest, nil
}

func parserVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dependency := range info.Deps {
			if dependency.Path == gdparserModule {
				if dependency.Replace != nil {
					return dependency.Replace.Path + "@" + dependency.Replace.Version
				}
				return dependency.Path + "@" + dependency.Version
			}
		}
	}
	return gdparserModule + "@unknown"
}
