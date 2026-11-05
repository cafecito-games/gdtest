// Package astcache stores parsed GDScript syntax trees by worktree-neutral
// source identity so static analysis can reuse unchanged parser work.
package astcache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdtest/internal/cache"
)

const schemaVersion = "gdscript-ast-v1"

// Parser loads or computes syntax trees with the exact configured parser
// identity. It never places absolute worktree paths in an artifact or key.
type Parser struct {
	store         *cache.Store
	toolVersion   string
	parserVersion string
	parse         func(string, []byte) (*ast.File, error)
}

type artifact struct {
	Schema     string
	Path       string
	SourceHash string
	Tree       *ast.File
}

var registerOnce sync.Once

// New creates a reusable AST parser backed by store.
func New(store *cache.Store, toolVersion, parserVersion string) (*Parser, error) {
	if store == nil {
		return nil, errors.New("AST cache store is nil")
	}
	if strings.TrimSpace(toolVersion) == "" || strings.TrimSpace(parserVersion) == "" {
		return nil, errors.New("AST cache tool and parser versions are required")
	}
	registerOnce.Do(registerASTTypes)
	return &Parser{
		store: store, toolVersion: toolVersion, parserVersion: parserVersion,
		parse: gdparser.ParseFile,
	}, nil
}

// Parse returns a verified cached tree or parses and atomically publishes one.
func (p *Parser) Parse(ctx context.Context, relative string, source []byte) (*ast.File, error) {
	if err := validateRelativePath(relative); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hash := sourceHash(source)
	configuration := []byte(schemaVersion + "\x00" + relative)
	key := cache.Key(p.toolVersion, p.parserVersion, configuration, source)
	if data, hit, err := p.store.Get(ctx, key); err != nil {
		return nil, err
	} else if hit {
		tree, decodeErr := decode(data, relative, hash)
		if decodeErr == nil {
			return tree, nil
		}
		if deleteErr := p.store.Delete(ctx, key); deleteErr != nil {
			return nil, errors.Join(decodeErr, deleteErr)
		}
	}
	data, err := p.store.GetOrCompute(ctx, key, func(computeCtx context.Context) ([]byte, error) {
		if err := computeCtx.Err(); err != nil {
			return nil, err
		}
		tree, parseErr := p.parse(relative, source)
		if parseErr != nil {
			return nil, parseErr
		}
		return encode(artifact{Schema: schemaVersion, Path: relative, SourceHash: hash, Tree: tree})
	})
	if err != nil {
		return nil, err
	}
	tree, err := decode(data, relative, hash)
	if err != nil {
		_ = p.store.Delete(ctx, key)
		return nil, err
	}
	return tree, nil
}

func encode(value artifact) ([]byte, error) {
	var output bytes.Buffer
	if err := gob.NewEncoder(&output).Encode(value); err != nil {
		return nil, fmt.Errorf("encode cached GDScript AST: %w", err)
	}
	return output.Bytes(), nil
}

func decode(data []byte, relative, hash string) (*ast.File, error) {
	decoder := gob.NewDecoder(bytes.NewReader(data))
	var value artifact
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode cached GDScript AST: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode cached GDScript AST: trailing content")
	}
	if value.Schema != schemaVersion || value.Path != relative || value.SourceHash != hash ||
		value.Tree == nil || value.Tree.Name != relative {
		return nil, errors.New("decode cached GDScript AST: identity mismatch")
	}
	return value.Tree, nil
}

func sourceHash(source []byte) string {
	sum := sha256.Sum256(source)
	return hex.EncodeToString(sum[:])
}

func validateRelativePath(value string) error {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, `\`) ||
		strings.HasPrefix(value, "res://") || strings.HasPrefix(value, "/") {
		return fmt.Errorf("AST cache path %q must be project-relative", value)
	}
	clean := filepath.ToSlash(filepath.Clean(value))
	if clean != value || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("AST cache path %q must be normalized", value)
	}
	return nil
}

func registerASTTypes() {
	for _, value := range []any{
		&ast.Annotation{}, &ast.ArrayLiteral{}, &ast.Assignment{},
		&ast.BinaryExpression{}, &ast.BindingPattern{}, &ast.CallExpression{},
		&ast.ClassDeclaration{}, &ast.Comment{}, &ast.DictionaryLiteral{}, &ast.Directive{},
		&ast.EnumDeclaration{}, &ast.ExpressionStatement{}, &ast.ForStatement{},
		&ast.FunctionDeclaration{}, &ast.Identifier{}, &ast.IfStatement{},
		&ast.KeywordStatement{}, &ast.LambdaExpression{}, &ast.Literal{},
		&ast.MatchStatement{}, &ast.MemberExpression{}, &ast.NodePathExpression{},
		&ast.RestPattern{}, &ast.ReturnStatement{}, &ast.SignalDeclaration{},
		&ast.SubscriptExpression{}, &ast.TernaryExpression{}, &ast.TypeExpression{},
		&ast.UnaryExpression{}, &ast.VariableDeclaration{}, &ast.WhileStatement{},
		&ast.WildcardPattern{},
	} {
		gob.Register(value)
	}
}
