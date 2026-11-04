package discovery

import (
	"strings"
	"testing"

	"github.com/cafecito-games/gdtest/internal/model"
)

func TestParsePreservesMethodSourceOrderAndStableIDs(t *testing.T) {
	t.Parallel()

	source := []byte(`extends GDTest

func test_second_name() -> void:
	pass

func helper() -> void:
	pass

func test_first_alphabetically__only() -> void:
	pass
`)
	manifest, err := Parse("tests/example.test.gd", source)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(manifest.Cases) != 2 {
		t.Fatalf("cases = %d, want 2", len(manifest.Cases))
	}
	if manifest.Cases[0].ID != "tests/example.test.gd::test_second_name" {
		t.Fatalf("first ID = %q", manifest.Cases[0].ID)
	}
	if manifest.Cases[1].ID != "tests/example.test.gd::test_first_alphabetically" || !manifest.Cases[1].IsOnly {
		t.Fatalf("focused case = %#v", manifest.Cases[1])
	}
}

func TestParseRejectsArgumentsWithoutProvider(t *testing.T) {
	t.Parallel()

	_, err := Parse("bad.test.gd", []byte("func test_bad(value: int) -> void:\n\tpass\n"))
	if err == nil || !strings.Contains(err.Error(), "test_bad__cases") {
		t.Fatalf("Parse error = %v, want provider diagnostic", err)
	}
}

func TestSelectUnknownSelector(t *testing.T) {
	t.Parallel()

	file, err := Parse("one.test.gd", []byte("func test_one() -> void:\n\tpass\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Select(Manifest{Files: []model.FilePlan{file}}, []string{"missing"}, false)
	if err == nil {
		t.Fatal("Select succeeded for unknown selector")
	}
}

func TestDoubleStarMatchesNestedAndRootFiles(t *testing.T) {
	t.Parallel()

	for _, filename := range []string{"root.test.gd", "one/root.test.gd", "one/two/root.test.gd"} {
		if !globMatch("**/*.test.gd", filename) {
			t.Errorf("pattern did not match %q", filename)
		}
	}
}
