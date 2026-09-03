package prismatic

import (
	"os"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/testutil"
)

// TestMain isolates HOME/XDG for the whole package before any test runs.
// credentials.go resolves its storage path via internal/agentpaths, which
// reads $HOME/$XDG_DATA_HOME — without this, `go test ./internal/prismatic/...`
// would read and write the developer's real ~/.local/share/agent-deck/prismatic/
// credentials.json (see internal/testutil/homeenv.go's incident history;
// every package that touches agentpaths needs this). detect_test.go's tests
// don't need it (Detect/FindRoot take an explicit path, no HOME involved),
// but it's harmless for them and required for credentials_test.go.
func TestMain(m *testing.M) { os.Exit(runTestMain(m)) }

func runTestMain(m *testing.M) int {
	cleanupHome := testutil.IsolateHome()
	defer cleanupHome()
	return m.Run()
}
