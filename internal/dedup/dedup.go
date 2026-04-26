package dedup

import (
	"fmt"
	"io"

	"github.com/eddyvarelae/media-vault/internal/manifest"
)

// PrintDuplicates writes a human-readable dedup report to w.
func PrintDuplicates(w io.Writer, groups []manifest.DuplicateGroup) {
	if len(groups) == 0 {
		fmt.Fprintln(w, "No duplicates.")
		return
	}

	var totalWasted int64
	for _, g := range groups {
		totalWasted += g.WastedBytes
	}

	fmt.Fprintf(w, "Found %d duplicate groups, %s reclaimable.\n\n",
		len(groups), human(totalWasted))

	for _, g := range groups {
		fmt.Fprintf(w, "%s  (%d copies, %s each, %s wasted)\n",
			g.SHA256[:12], len(g.Locations), human(g.Size), human(g.WastedBytes))
		for _, l := range g.Locations {
			fmt.Fprintf(w, "    [%s] %s\n", l.Disk, l.Path)
		}
		fmt.Fprintln(w)
	}
}

// PrintUnique writes the list of files unique to a single disk.
func PrintUnique(w io.Writer, disk string, entries []manifest.Entry) {
	if len(entries) == 0 {
		fmt.Fprintf(w, "No unique files in %q — every file in this disk has a copy elsewhere.\n", disk)
		return
	}

	var totalSize int64
	for _, e := range entries {
		totalSize += e.Size
	}

	fmt.Fprintf(w, "%d files in %q have NO copy elsewhere (%s).\n\n",
		len(entries), disk, human(totalSize))
	for _, e := range entries {
		fmt.Fprintf(w, "  %s  %s\n", human(e.Size), e.SourcePath)
	}
}

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for nn := n / unit; nn >= unit; nn /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
