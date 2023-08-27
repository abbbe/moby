package stream

import (
	"bytes"
	"testing"
)

func TestTransformWriter_Write(t *testing.T) {
	tests := []struct {
		name            string
		input           []string
		transformations []Transformation
		expectedOutput  []string
	}{
		{
			name: "single-line transformation",
			input: []string{
				"The black cat is on the red mat.",
			},
			transformations: []Transformation{
				{Search: []byte("black"), Replacement: []byte("white")},
				{Search: []byte("red"), Replacement: []byte("green")},
			},
			expectedOutput: []string{
				"The white cat is on the green mat.",
			},
		},
		{
			name: "multi-line transformation",
			input: []string{
				"The black cat is on the red mat.",
				"The red cat is on the black mat.",
			},
			transformations: []Transformation{
				{Search: []byte("black"), Replacement: []byte("white")},
				{Search: []byte("red"), Replacement: []byte("green")},
			},
			expectedOutput: []string{
				"The white cat is on the green mat.",
				"The green cat is on the white mat.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tw := &TransformWriter{
				w:               &buf,
				transformations: tt.transformations,
			}

			for i, chunk := range tt.input {
				_, err := tw.Write([]byte(chunk))
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if buf.String() != tt.expectedOutput[i] {
					t.Fatalf("expected %q but got %q", tt.expectedOutput[i], buf.String())
				}
				buf.Reset()
			}
		})
	}
}
