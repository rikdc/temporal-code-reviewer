package activities

import (
	"testing"
)

func TestParsePatch(t *testing.T) {
	diff := `--- a/main.go
+++ b/main.go
@@ -10,6 +10,8 @@ package main
 
 import (
 	"fmt"
+	"log"
+	"os"
 )
 
 func main() {
@@ -20,3 +22,5 @@ func main() {
 	fmt.Println("hello")
+	fmt.Println("world")
+	os.Exit(0)
 }
`
	patch, err := ParsePatch(diff)
	if err != nil {
		t.Fatalf("ParsePatch failed: %v", err)
	}
	if patch.OrigFile != "a/main.go" {
		t.Errorf("OrigFile = %q, want %q", patch.OrigFile, "a/main.go")
	}
	if patch.NewFile != "b/main.go" {
		t.Errorf("NewFile = %q, want %q", patch.NewFile, "b/main.go")
	}
	if len(patch.Hunks) != 2 {
		t.Errorf("len(Hunks) = %d, want 2", len(patch.Hunks))
	}
}

func TestParsePatch_NoHunks(t *testing.T) {
	diff := `--- a/foo.go
+++ b/foo.go
`
	_, err := ParsePatch(diff)
	if err == nil {
		t.Error("patch with no hunks should fail")
	}
}

func TestParsePatch_NoHeaders(t *testing.T) {
	_, err := ParsePatch("just some text")
	if err == nil {
		t.Error("patch without headers should fail")
	}
}

func TestApplyPatch_Success(t *testing.T) {
	original := "line1\nline2\nline3\n"
	diff := `--- a/test.go
+++ b/test.go
@@ -1,3 +1,3 @@
 line1
-line2
+line2_modified
 line3
`
	patch, err := ParsePatch(diff)
	if err != nil {
		t.Fatalf("ParsePatch: %v", err)
	}

	result, err := ApplyPatch(original, patch)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	expected := "line1\nline2_modified\nline3\n"
	if result != expected {
		t.Errorf("ApplyPatch result =\n%s\nwant:\n%s", result, expected)
	}
}

func TestApplyPatch_ContextMismatch(t *testing.T) {
	original := "line1\nline2\nline3\n"
	diff := `--- a/test.go
+++ b/test.go
@@ -1,3 +1,3 @@
 line1
-line_wrong
+modified
 line3
`
	patch, err := ParsePatch(diff)
	if err != nil {
		t.Fatalf("ParsePatch: %v", err)
	}

	_, err = ApplyPatch(original, patch)
	if err == nil {
		t.Error("context mismatch should fail")
	}
}

func TestApplyPatch_Deletion(t *testing.T) {
	original := "line1\nline2\nline3\n"
	diff := `--- a/test.go
+++ b/test.go
@@ -1,3 +1,2 @@
 line1
-line2
 line3
`
	patch, err := ParsePatch(diff)
	if err != nil {
		t.Fatalf("ParsePatch: %v", err)
	}

	result, err := ApplyPatch(original, patch)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	expected := "line1\nline3\n"
	if result != expected {
		t.Errorf("ApplyPatch result =\n%s\nwant:\n%s", result, expected)
	}
}

func TestApplyPatch_Addition(t *testing.T) {
	original := "line1\nline3\n"
	diff := `--- a/test.go
+++ b/test.go
@@ -1,2 +1,3 @@
 line1
+line2
 line3
`
	patch, err := ParsePatch(diff)
	if err != nil {
		t.Fatalf("ParsePatch: %v", err)
	}

	result, err := ApplyPatch(original, patch)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	expected := "line1\nline2\nline3\n"
	if result != expected {
		t.Errorf("ApplyPatch result =\n%s\nwant:\n%s", result, expected)
	}
}

func TestValidatePatch_WrongFile(t *testing.T) {
	original := "line1\n"
	diff := `--- a/wrong.go
+++ b/wrong.go
@@ -1 +1 @@
-line1
+modified
`
	patch, err := ParsePatch(diff)
	if err != nil {
		t.Fatalf("ParsePatch: %v", err)
	}

	err = ValidatePatch(original, patch, "expected.go")
	if err == nil {
		t.Error("wrong file should fail validation")
	}
}

func TestApplyPatch_AdditionAtStart(t *testing.T) {
	original := "line2\nline3\n"
	diff := `--- a/test.go
+++ b/test.go
@@ -1,2 +1,3 @@
+line1
 line2
 line3
`
	patch, err := ParsePatch(diff)
	if err != nil {
		t.Fatalf("ParsePatch: %v", err)
	}

	result, err := ApplyPatch(original, patch)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	expected := "line1\nline2\nline3\n"
	if result != expected {
		t.Errorf("ApplyPatch result =\n%s\nwant:\n%s", result, expected)
	}
}

func TestApplyPatch_EmptyOriginal(t *testing.T) {
	original := ""
	diff := `--- a/test.go
+++ b/test.go
@@ -0 +1 @@
+new line
`
	patch, err := ParsePatch(diff)
	if err != nil {
		t.Fatalf("ParsePatch: %v", err)
	}

	result, err := ApplyPatch(original, patch)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	expected := "new line\n"
	if result != expected {
		t.Errorf("ApplyPatch result =\n%s\nwant:\n%s", result, expected)
	}
}
