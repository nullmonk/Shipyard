package transfer

import (
	"reflect"
	"strings"
	"testing"
)

type mockDriver struct {
	sources    map[string][]string // container:pattern -> matches
	isDirMap   map[string]bool     // container:path -> isDir
	isDirTgt   map[string]bool     // container:path -> isDirTarget
	executed   []Task
}

func newMockDriver() *mockDriver {
	return &mockDriver{
		sources:  make(map[string][]string),
		isDirMap: make(map[string]bool),
		isDirTgt: make(map[string]bool),
	}
}

func (m *mockDriver) ExpandSource(container, srcPath string) ([]string, bool, error) {
	key := container + ":" + srcPath
	if matches, ok := m.sources[key]; ok {
		isGlob := strings.ContainsAny(srcPath, "*?[]")
		return matches, isGlob, nil
	}
	isGlob := strings.ContainsAny(srcPath, "*?[]")
	return []string{srcPath}, isGlob, nil
}

func (m *mockDriver) IsDir(container, path string) (bool, error) {
	return m.isDirMap[container+":"+path], nil
}

func (m *mockDriver) IsDirTarget(container, path string) (bool, error) {
	if strings.HasSuffix(path, "/") || path == "." || path == "" {
		return true, nil
	}
	return m.isDirTgt[container+":"+path], nil
}

func (m *mockDriver) RunTasks(tasks []Task) error {
	m.executed = append(m.executed, tasks...)
	return nil
}

func TestPathResolutionMatrix(t *testing.T) {
	tests := []struct {
		name        string
		callType    string // "copy" or "save"
		args        []string
		setup       func(m *mockDriver)
		wantErr     bool
		errContains string
		wantTasks   []Task
	}{
		{
			name:     "Row 1: Copy('pkg/*')",
			callType: "copy",
			args:     []string{"pkg/*"},
			setup: func(m *mockDriver) {
				m.sources["context:pkg/*"] = []string{"pkg/a", "pkg/b"}
				m.isDirMap["context:pkg/a"] = false
				m.isDirMap["context:pkg/b"] = false
			},
			wantTasks: []Task{
				{SrcContainer: "context", SrcPath: "pkg/a", DstContainer: "current", DstPath: "a", IsDir: false},
				{SrcContainer: "context", SrcPath: "pkg/b", DstContainer: "current", DstPath: "b", IsDir: false},
			},
		},
		{
			name:     "Row 2: Copy('pkg/')",
			callType: "copy",
			args:     []string{"pkg/"},
			setup: func(m *mockDriver) {
				m.isDirMap["context:pkg/"] = true
			},
			wantTasks: []Task{
				{SrcContainer: "context", SrcPath: "pkg/", DstContainer: "current", DstPath: "pkg", IsDir: true},
			},
		},
		{
			name:     "Row 3: Copy('pkg')",
			callType: "copy",
			args:     []string{"pkg"},
			setup: func(m *mockDriver) {
				m.isDirMap["context:pkg"] = true
			},
			wantTasks: []Task{
				{SrcContainer: "context", SrcPath: "pkg", DstContainer: "current", DstPath: "pkg", IsDir: true},
			},
		},
		{
			name:     "Row 4: Copy('pkg/.', 'out/')",
			callType: "copy",
			args:     []string{"pkg/.", "out/"},
			setup: func(m *mockDriver) {
				m.sources["context:pkg/*"] = []string{"pkg/a", "pkg/b"}
				m.isDirMap["context:pkg/a"] = false
				m.isDirMap["context:pkg/b"] = false
			},
			wantTasks: []Task{
				{SrcContainer: "context", SrcPath: "pkg/a", DstContainer: "current", DstPath: "out/a", IsDir: false},
				{SrcContainer: "context", SrcPath: "pkg/b", DstContainer: "current", DstPath: "out/b", IsDir: false},
			},
		},
		{
			name:     "Row 5: Copy('main.go')",
			callType: "copy",
			args:     []string{"main.go"},
			setup: func(m *mockDriver) {
				m.isDirMap["context:main.go"] = false
			},
			wantTasks: []Task{
				{SrcContainer: "context", SrcPath: "main.go", DstContainer: "current", DstPath: "main.go", IsDir: false},
			},
		},
		{
			name:     "Row 6: Copy('main.go', 'app.go')",
			callType: "copy",
			args:     []string{"main.go", "app.go"},
			setup: func(m *mockDriver) {
				m.isDirMap["context:main.go"] = false
				m.isDirTgt["current:app.go"] = false
			},
			wantTasks: []Task{
				{SrcContainer: "context", SrcPath: "main.go", DstContainer: "current", DstPath: "app.go", IsDir: false},
			},
		},
		{
			name:     "Row 7: Copy('foo:src/*', 'bar:dst/')",
			callType: "copy",
			args:     []string{"foo:src/*", "bar:dst/"},
			setup: func(m *mockDriver) {
				m.sources["foo:src/*"] = []string{"src/x", "src/y"}
			},
			wantTasks: []Task{
				{SrcContainer: "foo", SrcPath: "src/x", DstContainer: "bar", DstPath: "dst/x", IsDir: false},
				{SrcContainer: "foo", SrcPath: "src/y", DstContainer: "bar", DstPath: "dst/y", IsDir: false},
			},
		},
		{
			name:     "Row 8: Save('build/*')",
			callType: "save",
			args:     []string{"build/*"},
			setup: func(m *mockDriver) {
				m.sources["current:build/*"] = []string{"build/artifact1", "build/artifact2"}
			},
			wantTasks: []Task{
				{SrcContainer: "current", SrcPath: "build/artifact1", DstContainer: "artifacts", DstPath: "artifact1", IsDir: false},
				{SrcContainer: "current", SrcPath: "build/artifact2", DstContainer: "artifacts", DstPath: "artifact2", IsDir: false},
			},
		},
		{
			name:     "Row 9: Save('bin', 'release/')",
			callType: "save",
			args:     []string{"bin", "release/"},
			setup: func(m *mockDriver) {
				m.isDirMap["current:bin"] = true
			},
			wantTasks: []Task{
				{SrcContainer: "current", SrcPath: "bin", DstContainer: "artifacts", DstPath: "release/bin", IsDir: true},
			},
		},
		{
			name:        "Row 10: Save('bin', 'other:rel/') - Target container locked",
			callType:    "save",
			args:        []string{"bin", "other:rel/"},
			wantErr:     true,
			errContains: "container override not permitted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drv := newMockDriver()
			if tt.setup != nil {
				tt.setup(drv)
			}
			ctx := NewContext(drv, "current", "context", "artifacts")

			var err error
			if tt.callType == "copy" {
				if len(tt.args) == 1 {
					err = ctx.Copy(tt.args[0])
				} else {
					err = ctx.Copy(tt.args[0], tt.args[1:]...)
				}
			} else {
				if len(tt.args) == 1 {
					err = ctx.Save(tt.args[0])
				} else {
					err = ctx.Save(tt.args[0], tt.args[1:]...)
				}
			}

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !reflect.DeepEqual(drv.executed, tt.wantTasks) {
				t.Errorf("executed tasks mismatch:\ngot  %+v\nwant %+v", drv.executed, tt.wantTasks)
			}
		})
	}
}

