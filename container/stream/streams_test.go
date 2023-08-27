package stream

import (
        "regexp"
        "testing"
)


func TestApplyTransformations(t *testing.T) {
    transformations := []Transformation{
        {Pattern: regexp.MustCompile("white"), Replacement: "black"},
    }
    input := "This is a white test string."
    expected := "This is a black test string."
    result := applyTransformations(input, transformations)
    if result != expected {
        t.Fatalf("Expected %s but got %s", expected, result)
    }
}
