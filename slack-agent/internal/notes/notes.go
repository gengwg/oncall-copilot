// Package notes keeps the owner's own findings per node, so later briefs on
// that node start from them. The agent writes; the poller reads them into the
// model prompt (alert-relay/investigate.sh), so the file name and line format
// are shared with that script.
package notes

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxText caps one note. Longer text is cut, not refused.
const MaxText = 2000

type Note struct {
	TS        time.Time
	Ref       string // the alert it was written on, as shown to the owner, e.g. "IRM #5611"
	Alertname string
	Text      string
}

// ErrOff means no notes directory is configured.
var ErrOff = errors.New("notes are off (NOTES_DIR is unset)")

// Store is a directory of <cluster>@<node>.tsv files, one note per line:
// ts, ref, alertname, text. The directory belongs to the poller's user and
// group; files are group-readable so the poller can read them.
type Store struct{ Dir string }

// Key is the file name stem for a cluster and node. It sanitizes each byte the
// way investigate.sh does (tr -c 'A-Za-z0-9_.-' '_'), so both sides agree.
func Key(cluster, node string) string { return sanitize(cluster) + "@" + sanitize(node) }

func sanitize(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-') {
			b[i] = '_'
		}
	}
	return string(b)
}

func (s Store) path(cluster, node string) (string, error) {
	if s.Dir == "" {
		return "", ErrOff
	}
	return filepath.Join(s.Dir, Key(cluster, node)+".tsv"), nil
}

// flat keeps a field on one TSV line.
func flat(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(s)), " ")
}

func (s Store) Append(cluster, node string, n Note) error {
	p, err := s.path(cluster, node)
	if err != nil {
		return err
	}
	text := flat(n.Text)
	if len(text) > MaxText {
		text = strings.ToValidUTF8(text[:MaxText], "")
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%s\t%s\t%s\t%s\n", n.TS.UTC().Format(time.RFC3339), flat(n.Ref), flat(n.Alertname), text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// List returns the node's notes, oldest first. No file means no notes.
func (s Store) List(cluster, node string) ([]Note, error) {
	p, err := s.path(cluster, node)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Note
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for sc.Scan() {
		p := strings.SplitN(sc.Text(), "\t", 4)
		if len(p) != 4 {
			continue
		}
		ts, err := time.Parse(time.RFC3339, p[0])
		if err != nil {
			continue
		}
		out = append(out, Note{TS: ts, Ref: p[1], Alertname: p[2], Text: p[3]})
	}
	return out, sc.Err()
}
