package file

import (
	"fmt"
	"os"
)

// Move copies a file from src to dest and then removes the original.
// This implementation is cross-platform and provides better error handling.
func Move(src, dest string) error {
	// Check if source file exists
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return fmt.Errorf("source file does not exist: %s", src)
	}

	// Copy the file content
	input, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read source file: %w", err)
	}

	// Write the content to destination
	if err = os.WriteFile(dest, input, 0644); err != nil {
		return fmt.Errorf("failed to write to destination file: %w", err)
	}

	// Remove the original file
	if err = os.Remove(src); err != nil {
		return fmt.Errorf("failed to remove source file: %w", err)
	}

	return nil
}
