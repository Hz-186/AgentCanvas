package memory_usecase

import (
	"fmt"
	"os"

	"agentcanvas/internal/domain/memory"
)

// HasExplicitMemoryIntent recognizes only direct user requests. Ordinary
// statements are deliberately excluded so a ReAct answer cannot create a
// durable note accidentally.
func HasExplicitMemoryIntent(value string) bool {
	return memory.HasExplicitMemoryIntent(value)
}

// checkDurableDir validates an existing directory below the configured memory
// root and rejects symlink components. Neither the migration importer nor the
// cleanup stage creates directories: a missing owner workspace is a normal
// empty-memory state.
func checkDurableDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("durable memory path must not be a symlink: %s", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("durable memory path is not a directory: %s", path)
	}
	return nil
}
