package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv reads a KEY=VALUE file into the process environment without
// overwriting variables that are already set.
//
// Written by hand rather than pulled from a dependency: the format is twenty
// lines of parsing, and a document service that handles business data is better
// off with an auditable zero-dependency build.
func LoadDotEnv(path string) error { return loadDotEnv(path, false) }

// ForceDotEnv reads the same file, but the file wins over the environment.
//
// The escape hatch for a machine whose environment cannot be corrected. On the
// deployment box a stale DOCFIND_AI_MODEL sat in the machine environment from
// an earlier setup and silently outranked a corrected .env -- and it could not
// simply be deleted, because the Task Scheduler service caches its copy of the
// environment block and keeps handing the old value to the task until the
// machine is restarted. Rebooting a server to change a model name is not a
// reasonable ask, and editing the task's command line is immediate.
//
// So this exists to be turned on deliberately, per invocation, and never by
// default: the ordinary rule -- environment beats file -- is what lets an
// operator override a checked-in file without editing it, and that is worth
// keeping.
func ForceDotEnv(path string) error { return loadDotEnv(path, true) }

func loadDotEnv(path string, force bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])

		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				quote := val[0]
				val = val[1 : len(val)-1]
				if quote == '"' {
					val = strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\"`, `"`, `\\`, `\`).Replace(val)
				}
			} else if i := strings.Index(val, " #"); i >= 0 {
				val = strings.TrimSpace(val[:i]) // trailing comment on an unquoted value
			}
		}
		if _, exists := os.LookupEnv(key); force || !exists {
			os.Setenv(key, val)
		}
	}
	return sc.Err()
}
