package shipfile

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	shipmod "shipyard/pkg/shipfile/modules/shipyard"

	"github.com/go-python/gpython/py"
)

// Shipfile represents the parsed configuration of a shipfile.py.
type Shipfile struct {
	Path              string
	Dir               string
	Name              string
	Url               string
	Urls              []string
	Branch            string
	VersionTags       string
	IgnoredVersions   []string
	PackageName       string
	Repository        string
	BaseImage         string
	Artifacts         string
	ArtifactDir       string
	Variables         []shipmod.Variable
	Dependencies      []Dependency
	Patches           string
	HasCodePatches    bool
	HasPatchFiles     bool
	ImplementsPatcher bool
	ImplementsBuilder bool
	ImplementsTester  bool
	HasPackageName    bool
	HasPrePatch       bool
	HasPostPatch      bool
	HasSetup          bool
	HasBuild          bool
	HasSave           bool
	HasTests          bool
	HasPreTest        bool
	HasPostTest       bool

	sfClass py.Object
	sfInst  py.Object
	interp  *Interpreter
}

// IsBuild returns true if this shipfile implements the Builder subclass.
func (s *Shipfile) IsBuild() bool {
	return s.ImplementsBuilder
}

// IsPatch returns true if this shipfile implements the Patcher subclass.
func (s *Shipfile) IsPatch() bool {
	return s.ImplementsPatcher
}

// IsTest returns true if this shipfile implements the Tester subclass.
func (s *Shipfile) IsTest() bool {
	return s.ImplementsTester
}

// IsGeneric returns true if this shipfile builds itself in a plain container
// rather than using a distro package manager.
func (s *Shipfile) IsGeneric() bool {
	return s.IsBuild()
}

// ValidateVariable runs the custom validator for a variable if defined.
func (s *Shipfile) ValidateVariable(name, value string) error {
	for i := range s.Variables {
		if s.Variables[i].Name == name {
			if err := s.Variables[i].Validate(value); err != nil {
				return fmt.Errorf("variable %q validation failed: %w", name, err)
			}
			return nil
		}
	}
	return fmt.Errorf("unknown variable %q", name)
}

// VariablesMap returns a map[name]value for all variables, applying any overrides.
func (s *Shipfile) VariablesMap(overrides ...map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(s.Variables))
	byName := make(map[string]*shipmod.Variable, len(s.Variables))
	for i := range s.Variables {
		v := &s.Variables[i]
		out[v.Name] = v.Value
		byName[v.Name] = v
	}
	for _, ov := range overrides {
		for k, val := range ov {
			v, exists := byName[k]
			if !exists {
				return nil, fmt.Errorf("unknown variable %q", k)
			}
			if err := v.Validate(val); err != nil {
				return nil, err
			}
			out[k] = val
		}
	}
	return out, nil
}

// NewContext creates a Python shipyard.Context object with version and ctx.variables populated as a dict.
// All variable values are validated and resolved before the Context object is returned.
func (s *Shipfile) NewContext(version string, overrides ...map[string]string) (py.Object, error) {
	if s.interp == nil {
		return nil, fmt.Errorf("shipfile interpreter is not initialized")
	}

	// Build a name→value override map and check for unknown variables.
	overrideMap := make(map[string]string)
	byName := make(map[string]*shipmod.Variable, len(s.Variables))
	for i := range s.Variables {
		byName[s.Variables[i].Name] = &s.Variables[i]
	}
	for _, ov := range overrides {
		for k, val := range ov {
			if _, exists := byName[k]; !exists {
				return nil, fmt.Errorf("unknown variable %q", k)
			}
			overrideMap[k] = val
		}
	}

	// Build a Python dict of variable name -> validated and resolved value.
	varsDict := py.NewStringDict()
	for i := range s.Variables {
		v := &s.Variables[i]
		val := v.Value
		if ovVal, ok := overrideMap[v.Name]; ok {
			val = ovVal
		}
		resolved, err := v.ValidateAndResolve(val)
		if err != nil {
			return nil, err
		}
		varsDict[v.Name] = resolved
	}

	ctxCls, err := s.interp.GetShipyardAttr("Context")
	if err != nil {
		return nil, fmt.Errorf("get shipyard.Context: %w", err)
	}
	kwargs := py.StringDict{
		"version":   py.String(version),
		"variables": varsDict,
	}
	return py.Call(ctxCls, py.Tuple{}, kwargs)
}

// CallMethod creates a Context with ctx.variables populated, attaches it to inst.ctx, and invokes methodName on inst.
func (s *Shipfile) CallMethod(inst py.Object, methodName string, overrides ...map[string]string) error {
	if inst == nil {
		inst = s.sfInst
	}
	ctxObj, err := s.NewContext("", overrides...)
	if err != nil {
		return err
	}
	if inst != nil && ctxObj != nil {
		_, _ = py.SetAttrString(inst, "ctx", ctxObj)
	}

	fn, found := findMethodInType(s.sfClass, inst, methodName)
	if !found {
		return fmt.Errorf("no method %s found on Shipfile instance", methodName)
	}

	if err := callMethod(fn, inst); err != nil {
		py.TracebackDump(err)
		return fmt.Errorf("%s() failed: %v", methodName, err)
	}
	return nil
}

// findMethodInType walks the class MRO to find an unbound function by name.
func findMethodInType(sfClass, inst py.Object, name string) (py.Object, bool) {
	var typ *py.Type
	if t, ok := sfClass.(*py.Type); ok && t.Name != "" {
		typ = t
	} else if inst != nil {
		if t, ok := inst.(*py.Type); ok {
			if t.Name != "" {
				typ = t
			} else {
				typ = t.ObjectType
			}
		} else {
			typ = inst.Type()
		}
	}
	if typ == nil {
		return nil, false
	}
	for _, base := range typ.Mro {
		b, ok := base.(*py.Type)
		if !ok {
			continue
		}
		if val, exists := b.Dict[name]; exists {
			return unwrapCallable(val), true
		}
	}
	return nil, false
}

// callMethod calls a Shipfile method with inst (self).
func callMethod(fn, inst py.Object) error {
	_, err := py.Call(fn, py.Tuple{inst}, nil)
	return err
}

// callCodePatch calls a @CodePatch function with (self, file) or (file).
func callCodePatch(fn, inst, cfObj py.Object) error {
	if pyFn, ok := fn.(*py.Function); ok && pyFn.Code != nil {
		argc := int(pyFn.Code.Argcount)
		hasSelf := argc > 0 && len(pyFn.Code.Varnames) > 0 && pyFn.Code.Varnames[0] == "self"
		if hasSelf {
			_, err := py.Call(fn, py.Tuple{inst, cfObj}, nil)
			return err
		}
		_, err := py.Call(fn, py.Tuple{cfObj}, nil)
		return err
	}
	_, err := py.Call(fn, py.Tuple{inst, cfObj}, nil)
	return err
}

// RunCodePatches executes the Shipfile's pre_patch, @CodePatch functions, and post_patch
// against sourceDir in gpython with variables set as a dict on the ctx object.
// Progress (hooks and per-file patch invocations) is written to log; pass io.Discard to suppress.
func (s *Shipfile) RunCodePatches(sourceDir, version string, log io.Writer, overrides ...map[string]string) ([]string, error) {
	if s.interp == nil {
		return nil, fmt.Errorf("shipfile interpreter is not initialized")
	}
	inst := s.sfInst
	if inst == nil && s.sfClass != nil {
		var err error
		inst, err = py.Call(s.sfClass, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("instantiate Shipfile: %w", err)
		}
		s.sfInst = inst
	}

	ctxObj, err := s.NewContext(version, overrides...)
	if err != nil {
		return nil, err
	}
	if inst != nil && ctxObj != nil {
		_, _ = py.SetAttrString(inst, "ctx", ctxObj)
	}

	sb := s.interp.Sandbox()
	prevFileRead := sb.FileReadEnabled()
	sb.SetFileRead(true)
	defer sb.SetFileRead(prevFileRead)

	if s.HasPrePatch && inst != nil {
		if fn, ok := findMethodInType(s.sfClass, inst, "pre_patch"); ok {
			fmt.Fprintln(log, "[*] hook: pre_patch")
			if err := callMethod(fn, inst); err != nil {
				return nil, fmt.Errorf("pre_patch failed: %w", err)
			}
		}
	}

	type cpEntry struct {
		name     string
		fn       py.Object
		patterns []*regexp.Regexp
		versions []string
		required bool
		hasRun   bool
	}

	var patches []*cpEntry
	if typ, ok := s.sfClass.(*py.Type); ok {
		collectFromDict := func(dict py.StringDict) {
			for key, val := range dict {
				fn := unwrapCallable(val)
				filesObj, err := py.GetAttrString(fn, "__patch_files")
				if err != nil {
					continue
				}
				var patterns []*regexp.Regexp
				if t, ok := filesObj.(py.Tuple); ok {
					for _, item := range t {
						if ps, ok := item.(py.String); ok {
							re, err := regexp.Compile("^(?:" + string(ps) + ")$")
							if err != nil {
								re, _ = regexp.Compile(string(ps))
							}
							if re != nil {
								patterns = append(patterns, re)
							}
						}
					}
				} else if l, ok := filesObj.(*py.List); ok {
					for _, item := range l.Items {
						if ps, ok := item.(py.String); ok {
							re, err := regexp.Compile("^(?:" + string(ps) + ")$")
							if err != nil {
								re, _ = regexp.Compile(string(ps))
							}
							if re != nil {
								patterns = append(patterns, re)
							}
						}
					}
				}
				if len(patterns) == 0 {
					continue
				}
				req := true
				if reqObj, err := py.GetAttrString(fn, "__patch_required"); err == nil {
					req = reqObj == py.True
				}
				var vers []string
				if vObj, err := py.GetAttrString(fn, "__patch_versions"); err == nil {
					if vt, ok := vObj.(py.Tuple); ok {
						for _, item := range vt {
							if vs, ok := item.(py.String); ok {
								vers = append(vers, string(vs))
							}
						}
					} else if vl, ok := vObj.(*py.List); ok {
						for _, item := range vl.Items {
							if vs, ok := item.(py.String); ok {
								vers = append(vers, string(vs))
							}
						}
					}
				}
				patches = append(patches, &cpEntry{
					name:     key,
					fn:       fn,
					patterns: patterns,
					versions: vers,
					required: req,
				})
			}
		}
		collectFromDict(typ.Dict)
		for _, base := range typ.Mro {
			b, ok := base.(*py.Type)
			if !ok || b == typ || isTargetClass(b, "Patcher") || isTargetClass(b, "Builder") || isTargetClass(b, "Shipfile") || b.Name == "object" {
				continue
			}
			collectFromDict(b.Dict)
		}
	}

	codeFileCls, _ := s.interp.GetShipyardAttr("CodeFile")

	appliedMap := make(map[string]bool)
	err = filepath.Walk(sourceDir, func(fullPath string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == ".pc" {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		relPath, err := filepath.Rel(sourceDir, fullPath)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		for _, cp := range patches {
			matched := false
			for _, re := range cp.patterns {
				if re.MatchString(relPath) || re.MatchString(strings.TrimPrefix(relPath, "/")) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			if len(cp.versions) > 0 && version != "" {
				verMatch := false
				for _, v := range cp.versions {
					if v == version {
						verMatch = true
						break
					}
				}
				if !verMatch {
					cp.hasRun = true
					continue
				}
			}

			cp.hasRun = true

			fmt.Fprintf(log, "[*] CodePatch %s: %s\n", cp.name, relPath)

			cfObj, err := py.Call(codeFileCls, py.Tuple{py.String(fullPath)}, nil)
			if err != nil {
				return fmt.Errorf("init CodeFile(%s): %w", relPath, err)
			}

			if callErr := callCodePatch(cp.fn, inst, cfObj); callErr != nil {
				return fmt.Errorf("code patch %s failed on %s: %w", cp.name, relPath, callErr)
			}
			appliedMap[cp.name] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, cp := range patches {
		if cp.required && !cp.hasRun {
			return nil, fmt.Errorf("code patch %q did not run and is required", cp.name)
		}
	}

	if s.HasPostPatch && inst != nil {
		if fn, ok := findMethodInType(s.sfClass, inst, "post_patch"); ok {
			fmt.Fprintln(log, "[*] hook: post_patch")
			if err := callMethod(fn, inst); err != nil {
				return nil, fmt.Errorf("post_patch failed: %w", err)
			}
		}
	}

	// Log raw patch files from patches/<version>/ if present.
	if s.Patches != "" && version != "" {
		patchesDir := s.Patches
		if !filepath.IsAbs(patchesDir) {
			patchesDir = filepath.Join(s.Dir, patchesDir)
		}
		versionDir := filepath.Join(patchesDir, version)
		if entries, err := os.ReadDir(versionDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && (strings.HasSuffix(e.Name(), ".patch") || strings.HasSuffix(e.Name(), ".diff")) {
					fmt.Fprintf(log, "[*] patch file: %s\n", e.Name())
				}
			}
		}
	}

	var applied []string
	for k := range appliedMap {
		applied = append(applied, k)
	}
	sort.Strings(applied)
	return applied, nil
}

// VariableDef represents a variable definition.
type VariableDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Value       string `json:"value"`
}

// GetVariables returns the list of variables with Name, Description, and Value.
func (s *Shipfile) GetVariables() []VariableDef {
	out := make([]VariableDef, 0, len(s.Variables))
	for _, v := range s.Variables {
		desc := v.Description
		if desc == "" {
			desc = v.Name
		}
		out = append(out, VariableDef{
			Name:        v.Name,
			Description: desc,
			Value:       v.Value,
		})
	}
	return out
}

// GetVariable returns a specific variable by name.
func (s *Shipfile) GetVariable(name string) (VariableDef, bool) {
	for _, v := range s.Variables {
		if v.Name == name {
			desc := v.Description
			if desc == "" {
				desc = v.Name
			}
			return VariableDef{
				Name:        v.Name,
				Description: desc,
				Value:       v.Value,
			}, true
		}
	}
	return VariableDef{}, false
}

// fstringRe matches the start of an f-string token (f" f' F" F' with optional triple quotes).
// A negative lookbehind is emulated by requiring a non-identifier character before the f/F.
var fstringRe = regexp.MustCompile(`(?:^|[^a-zA-Z0-9_])([fF](?:"""|'''|"|'))`)

// checkFStrings returns a descriptive error if source contains any f-string
// literals. gpython does not support them; use str.format() or % formatting.
func checkFStrings(source string) error {
	for i, line := range strings.Split(source, "\n") {
		stripped := strings.TrimSpace(line)
		if strings.HasPrefix(stripped, "#") {
			continue
		}
		// Strip trailing inline comment (best-effort; doesn't handle # inside strings).
		if idx := strings.Index(stripped, " #"); idx >= 0 {
			stripped = stripped[:idx]
		}
		if fstringRe.MatchString(stripped) {
			return fmt.Errorf("line %d: f-strings are not supported (gpython does not implement them); use str.format() or %%-formatting instead:\n  %s", i+1, strings.TrimSpace(line))
		}
	}
	return nil
}

// Find looks for a shipfile.py starting at dir and checking common candidate locations.
func Find(dir string) (string, error) {
	if dir == "" {
		dir = "."
	}
	fi, err := os.Stat(dir)
	if err == nil && !fi.IsDir() {
		return filepath.Abs(dir)
	}

	candidates := []string{
		filepath.Join(dir, "shipfile.py"),
		filepath.Join(".", "shipfile.py"),
		filepath.Join("..", "shipfile.py"),
		filepath.Join("../..", "shipfile.py"),
	}

	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return filepath.Abs(c)
		}
	}
	return "", fmt.Errorf("no shipfile.py found in %s or candidate search paths", dir)
}

// Load reads and parses a shipfile.py using the provided gpython interpreter.
func Load(path string, interp *Interpreter) (*Shipfile, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read shipfile %s: %w", absPath, err)
	}
	if err := checkFStrings(string(raw)); err != nil {
		return nil, fmt.Errorf("%s: %w", absPath, err)
	}

	mod, err := interp.RunShipfile(absPath)
	if err != nil {
		py.TracebackDump(err)
		return nil, fmt.Errorf("run shipfile %s: %w", absPath, err)
	}

	return loadModule(mod, absPath, interp)
}

// LoadString parses a shipfile from string content using the provided interpreter.
func LoadString(content string, interp *Interpreter) (*Shipfile, error) {
	if err := checkFStrings(content); err != nil {
		return nil, fmt.Errorf("shipfile.py: %w", err)
	}
	code, err := py.Compile(content+"\n", "shipfile.py", py.ExecMode, 0, true)
	if err != nil {
		return nil, fmt.Errorf("compile shipfile: %w", err)
	}
	mod, err := py.RunCode(interp.Context(), code, "shipfile.py", nil)
	if err != nil {
		py.TracebackDump(err)
		return nil, fmt.Errorf("run shipfile: %w", err)
	}
	return loadModule(mod, "", interp)
}

// ValidateString parses and validates a shipfile from string content.
func ValidateString(content string, interp *Interpreter) (*Shipfile, error) {
	sf, err := LoadString(content, interp)
	if err != nil {
		return nil, err
	}
	if err := sf.Validate(); err != nil {
		return sf, err
	}
	return sf, nil
}

func loadModule(mod *py.Module, absPath string, interp *Interpreter) (*Shipfile, error) {
	sfClass, ok := mod.Globals["Shipfile"]
	if !ok {
		if absPath != "" {
			return nil, fmt.Errorf("no Shipfile class found in %s", absPath)
		}
		return nil, fmt.Errorf("no Shipfile class found in shipfile content")
	}

	dir := "."
	if absPath != "" {
		dir = filepath.Dir(absPath)
	}

	sf := &Shipfile{
		Path:        absPath,
		Dir:         dir,
		Name:        getAttrString(sfClass, "Name"),
		Url:         firstNonEmpty(getAttrString(sfClass, "Url"), getAttrString(sfClass, "URL"), getAttrString(sfClass, "url")),
		Urls:        firstNonEmptySlice(getAttrStringSlice(sfClass, "Urls"), getAttrStringSlice(sfClass, "URLs"), getAttrStringSlice(sfClass, "URLS"), getAttrStringSlice(sfClass, "urls")),
		Branch:      getAttrString(sfClass, "Branch"),
		VersionTags: getAttrString(sfClass, "VersionTags"),
		PackageName: firstNonEmpty(getAttrString(sfClass, "PackageName"), getAttrString(sfClass, "Package"), getAttrString(sfClass, "package_name")),
		Repository:  getAttrString(sfClass, "Repository"),
		BaseImage:   getAttrString(sfClass, "BaseImage"),
		Artifacts:   getAttrString(sfClass, "Artifacts"),
		ArtifactDir: getAttrString(sfClass, "ArtifactDir"),
		Patches:     getAttrString(sfClass, "Patches"),
		sfClass:     sfClass,
		interp:      interp,
	}

	if sf.Patches == "" {
		sf.Patches = "patches/"
	}
	if sf.VersionTags == "" {
		sf.VersionTags = "*"
	}

	// Extract and validate Variables list on Shipfile class
	vars, err := shipmod.ExtractVariables(sfClass)
	if err != nil {
		return nil, fmt.Errorf("shipfile variables error: %w", err)
	}
	for _, v := range vars {
		sf.Variables = append(sf.Variables, *v)
	}

	inst, _ := py.Call(sfClass, nil, nil)
	sf.sfInst = inst
	if inst != nil {
		if initialCtx, err := sf.NewContext(""); err == nil && initialCtx != nil {
			_, _ = py.SetAttrString(inst, "ctx", initialCtx)
		}
	}

	if typ, ok := sfClass.(*py.Type); ok {
		checkDict := func(dict py.StringDict) {
			if !sf.HasCodePatches {
				for _, val := range dict {
					fn := unwrapCallable(val)
					if _, err := py.GetAttrString(fn, "__patch_files"); err == nil {
						sf.HasCodePatches = true
						break
					}
				}
			}
			if !sf.HasTests {
				for _, val := range dict {
					fn := unwrapCallable(val)
					if _, err := py.GetAttrString(fn, "__test_name"); err == nil {
						sf.HasTests = true
						break
					}
				}
			}
			if _, ok := dict["pre_patch"]; ok {
				sf.HasPrePatch = true
			}
			if _, ok := dict["post_patch"]; ok {
				sf.HasPostPatch = true
			}
			if _, ok := dict["pre_test"]; ok {
				sf.HasPreTest = true
			}
			if _, ok := dict["post_test"]; ok {
				sf.HasPostTest = true
			}
			if _, ok := dict["setup"]; ok {
				sf.HasSetup = true
			}
			if _, ok := dict["build"]; ok {
				sf.HasBuild = true
			}
			if _, ok := dict["save"]; ok {
				sf.HasSave = true
			}
			if _, ok := dict["package_name"]; ok {
				sf.HasPackageName = true
			}
		}

		checkDict(typ.Dict)
		for _, base := range typ.Mro {
			b, ok := base.(*py.Type)
			if !ok || b == typ {
				continue
			}
			if isTargetClass(b, "Patcher") || isTargetClass(b, "Builder") || isTargetClass(b, "Tester") || isTargetClass(b, "Shipfile") || b.Name == "object" {
				continue
			}
			checkDict(b.Dict)
		}
	}

	// Extract dependencies from Requires or Dependencies attribute
	var depObj py.Object
	if v, err := py.GetAttrString(sfClass, "Requires"); err == nil && v != nil && v != py.None {
		depObj = v
	} else if v, err := py.GetAttrString(sfClass, "Dependencies"); err == nil && v != nil && v != py.None {
		depObj = v
	} else if inst != nil {
		if v, err := py.GetAttrString(inst, "Requires"); err == nil && v != nil && v != py.None {
			depObj = v
		} else if v, err := py.GetAttrString(inst, "Dependencies"); err == nil && v != nil && v != py.None {
			depObj = v
		}
	}
	if depObj == nil || depObj == py.None {
		if v, ok := mod.Globals["Requires"]; ok && v != nil && v != py.None {
			depObj = v
		} else if v, ok := mod.Globals["Dependencies"]; ok && v != nil && v != py.None {
			depObj = v
		}
	}
	if depObj != nil && depObj != py.None {
		sf.Dependencies = parseDependencies(depObj)
	}

	if sf.PackageName != "" {
		sf.HasPackageName = true
	}

	// If sf.PackageName is empty but package_name method exists, try invoking it on an instance
	if sf.PackageName == "" && sf.HasPackageName {
		if inst != nil {
			if meth, err := py.GetAttrString(inst, "package_name"); err == nil {
				if res, err := py.Call(meth, nil, nil); err == nil {
					if s, ok := res.(py.String); ok && string(s) != "" {
						sf.PackageName = string(s)
					}
				}
			}
		}
	}

	// Detect raw patch files in the patches directory
	if sf.Dir != "" && sf.Patches != "" {
		patchesPath := sf.Patches
		if !filepath.IsAbs(patchesPath) {
			patchesPath = filepath.Join(sf.Dir, patchesPath)
		}
		if entries, err := os.ReadDir(patchesPath); err == nil {
			for _, e := range entries {
				if !e.IsDir() && (strings.HasSuffix(e.Name(), ".patch") || strings.HasSuffix(e.Name(), ".diff")) {
					sf.HasPatchFiles = true
					break
				}
			}
		}
	}

	// Detect subclass implementation
	sf.ImplementsPatcher = ImplementsSubclass(sfClass, "Patcher")
	sf.ImplementsBuilder = ImplementsSubclass(sfClass, "Builder")
	sf.ImplementsTester = ImplementsSubclass(sfClass, "Tester")

	return sf, nil
}

func parseDependencies(obj py.Object) []Dependency {
	if obj == nil || obj == py.None {
		return nil
	}
	var out []Dependency

	// 1. List or Tuple
	var items []py.Object
	if t, ok := obj.(py.Tuple); ok {
		items = t
	} else if l, ok := obj.(*py.List); ok {
		items = l.Items
	}

	if items != nil {
		for _, item := range items {
			if s, ok := item.(py.String); ok {
				out = append(out, parseStringDep(string(s)))
			} else if item != nil {
				dep := parseObjectDep(item)
				if len(dep.Names) > 0 {
					out = append(out, dep)
				}
			}
		}
		return out
	}

	// 2. Dict: {"curl": ">= 7.0", "bash": ""}
	if sDict, err := py.DictCheck(obj); err == nil {
		for k, v := range sDict {
			verStr := ""
			if s, ok := v.(py.String); ok {
				verStr = string(s)
			}
			out = append(out, parseStringDep(strings.TrimSpace(k+" "+verStr)))
		}
		return out
	}

	return nil
}

func parseStringDep(s string) Dependency {
	s = strings.TrimSpace(s)
	ops := []string{">=", "<=", "!=", "==", "=", ">", "<"}
	for _, op := range ops {
		idx := strings.Index(s, op)
		if idx != -1 {
			name := strings.TrimSpace(s[:idx])
			version := strings.TrimSpace(s[idx+len(op):])
			return Dependency{
				Names:   map[string]string{"": name},
				Op:      op,
				Version: Version(version),
			}
		}
	}
	return Dependency{
		Names: map[string]string{"": s},
	}
}

func parseObjectDep(item py.Object) Dependency {
	var namesMap map[string]string
	if namesAttr, err := py.GetAttrString(item, "names"); err == nil && namesAttr != nil && namesAttr != py.None {
		if sDict, err := py.DictCheck(namesAttr); err == nil {
			namesMap = make(map[string]string, len(sDict))
			for k, v := range sDict {
				if s, ok := v.(py.String); ok {
					namesMap[k] = string(s)
				} else {
					namesMap[k] = fmt.Sprint(v)
				}
			}
		}
	} else if nameAttr, err := py.GetAttrString(item, "name"); err == nil && nameAttr != nil && nameAttr != py.None {
		if s, ok := nameAttr.(py.String); ok {
			namesMap = map[string]string{"": string(s)}
		}
	}
	op := getAttrString(item, "op")
	version := getAttrString(item, "version")
	if len(namesMap) == 0 {
		return Dependency{}
	}
	return Dependency{
		Names:   namesMap,
		Op:      op,
		Version: Version(version),
	}
}

// LoadDefault creates an interpreter with default package root and parses the shipfile.
func LoadDefault(path string, pkgDir string) (*Shipfile, error) {
	if pkgDir == "" {
		pkgDir = "."
	}
	interp, err := New(pkgDir)
	if err != nil {
		return nil, fmt.Errorf("init interpreter: %w", err)
	}
	defer interp.Close()

	return Load(path, interp)
}

func getAttrString(obj py.Object, attr string) string {
	v, err := py.GetAttrString(obj, attr)
	if err != nil {
		return ""
	}
	if s, ok := v.(py.String); ok {
		return string(s)
	}
	return ""
}

func getAttrStringSlice(obj py.Object, attr string) []string {
	v, err := py.GetAttrString(obj, attr)
	if err != nil {
		return nil
	}
	var out []string
	switch t := v.(type) {
	case py.Tuple:
		for _, item := range t {
			if s, ok := item.(py.String); ok {
				out = append(out, string(s))
			}
		}
	case *py.List:
		for _, item := range t.Items {
			if s, ok := item.(py.String); ok {
				out = append(out, string(s))
			}
		}
	}
	return out
}

func getAttrStringMap(obj py.Object, attr string) map[string]string {
	v, err := py.GetAttrString(obj, attr)
	if err != nil {
		return nil
	}
	dict, ok := v.(py.StringDict)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(dict))
	for k, val := range dict {
		if s, ok := val.(py.String); ok {
			out[k] = string(s)
		} else {
			out[k] = fmt.Sprint(val)
		}
	}
	return out
}

func firstNonEmpty(items ...string) string {
	for _, s := range items {
		if s != "" {
			return s
		}
	}
	return ""
}

func unwrapCallable(val py.Object) py.Object {
	if sm, ok := val.(*py.StaticMethod); ok {
		return sm.Callable
	}
	if cm, ok := val.(*py.ClassMethod); ok {
		return cm.Callable
	}
	return val
}

func firstNonEmptySlice(slices ...[]string) []string {
	for _, s := range slices {
		if len(s) > 0 {
			return s
		}
	}
	return nil
}

// ImplementsSubclass checks if the Python class object inherits from targetClassName.
func ImplementsSubclass(classObj py.Object, targetClassName string) bool {
	typ, ok := classObj.(*py.Type)
	if !ok {
		return false
	}
	for _, base := range typ.Mro {
		if b, ok := base.(*py.Type); ok && b != typ {
			if isTargetClass(b, targetClassName) {
				return true
			}
		}
	}
	for _, base := range typ.Bases {
		if b, ok := base.(*py.Type); ok && b != typ {
			if isTargetClass(b, targetClassName) {
				return true
			}
		}
	}
	return false
}

func isTargetClass(t *py.Type, target string) bool {
	if t.Name == target || strings.HasSuffix(t.Name, "."+target) {
		return true
	}
	if t.Qualname == target || strings.HasSuffix(t.Qualname, "."+target) {
		return true
	}
	return false
}

// RunContext carries the inputs for a Patch or Build execution.
type RunContext struct {
	// SourceDir is the directory containing the source files to patch (required for Patch).
	SourceDir string
	// Version is the version string passed to CodePatch version gates and the Python Context.
	Version string
	// Variables overrides default variable values defined in the Shipfile.
	Variables map[string]string
	// Log receives progress messages (hooks and per-file patch invocations). Defaults to io.Discard.
	Log io.Writer
}

func (rc RunContext) overrides() []map[string]string {
	if len(rc.Variables) > 0 {
		return []map[string]string{rc.Variables}
	}
	return nil
}

func (rc RunContext) log() io.Writer {
	if rc.Log != nil {
		return rc.Log
	}
	return io.Discard
}

// Patch applies the shipfile's pre_patch, @CodePatch functions, and post_patch
// to the source tree at ctx.SourceDir. File read is temporarily enabled for
// the duration of the call. Returns the names of applied CodePatch functions.
func (s *Shipfile) Patch(ctx RunContext) ([]string, error) {
	return s.RunCodePatches(ctx.SourceDir, ctx.Version, ctx.log(), ctx.overrides()...)
}

func currentRunDepth() string {
	if d := os.Getenv(shipmod.RunDepthEnv); d != "" {
		return d
	}
	return "0"
}

func (s *Shipfile) enableBuildSandbox() (func(), error) {
	if s.interp == nil {
		return nil, fmt.Errorf("shipfile interpreter is not initialized")
	}
	sb := s.interp.Sandbox()
	prevExec := sb.ExecutionEnabled()
	prevFileRead := sb.FileReadEnabled()
	sb.SetExecution(true)
	sb.SetFileRead(true)
	sb.SetEnv(map[string]string{shipmod.RunDepthEnv: currentRunDepth()})
	return func() {
		sb.SetExecution(prevExec)
		sb.SetFileRead(prevFileRead)
	}, nil
}

// Setup invokes the shipfile's setup() method. Execution and file read are enabled
// for the duration of the call and restored afterwards.
func (s *Shipfile) Setup(ctx RunContext) error {
	cleanup, err := s.enableBuildSandbox()
	if err != nil {
		return err
	}
	defer cleanup()
	if s.HasSetup {
		if err := s.CallMethod(nil, "setup", ctx.overrides()...); err != nil {
			return fmt.Errorf("setup failed: %w", err)
		}
	}
	return nil
}

// RunWithBuilder sets b as the active ContainerBuilder and drives the
// shipfile's build lifecycle on the host side: setup() (if defined) then
// build(). Every shipyard.From/Run/Copy/Write/Source/Workdir call inside those
// methods routes through b to accumulate Dagger layers rather than executing
// locally. The caller reads the resulting container from b after this returns.
func (s *Shipfile) RunWithBuilder(b shipmod.ContainerBuilder, ctx RunContext) error {
	cleanup, err := s.enableBuildSandbox()
	if err != nil {
		return err
	}
	defer cleanup()

	shipmod.SetActiveBuilder(b)
	defer shipmod.ClearActiveBuilder()

	if s.HasSetup {
		if err := s.CallMethod(nil, "setup", ctx.overrides()...); err != nil {
			return fmt.Errorf("setup: %w", err)
		}
	}
	if s.HasBuild {
		if err := s.CallMethod(nil, "build", ctx.overrides()...); err != nil {
			return fmt.Errorf("build: %w", err)
		}
	}
	if s.HasSave {
		if err := s.CallMethod(nil, "save", ctx.overrides()...); err != nil {
			return fmt.Errorf("save: %w", err)
		}
	}
	return nil
}

// RunSaveWithBuilder is the Packer equivalent of the save() step. It sets b as
// the active builder (with workdir already pointing at the artifact directory),
// then calls save() so the shipfile can declare what to export via Save().
func (s *Shipfile) RunSaveWithBuilder(b shipmod.ContainerBuilder, ctx RunContext) error {
	if !s.HasSave {
		return nil
	}
	shipmod.SetActiveBuilder(b)
	defer shipmod.ClearActiveBuilder()
	if err := s.CallMethod(nil, "save", ctx.overrides()...); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	return nil
}

// SetupHash runs setup() in recording mode — Run() and Script() calls are
// captured rather than executed — and returns a SHA-256 hex digest of the
// captured commands. The hash changes only when the commands inside setup()
// change, not when @CodePatch or other unrelated methods are edited.
// Note: data-dependent branching on Run() output is not captured; the hash
// reflects the commands that would be reached with empty stdout/returncode=0.
func (s *Shipfile) SetupHash(ctx RunContext) (string, error) {
	if !s.HasSetup {
		return "", nil
	}
	shipmod.StartRecording()
	err := s.CallMethod(nil, "setup", ctx.overrides()...)
	cmds := shipmod.StopRecording()
	if err != nil {
		return "", fmt.Errorf("setup hash: %w", err)
	}
	h := sha256.New()
	for _, cmd := range cmds {
		fmt.Fprintln(h, cmd)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// Build invokes the shipfile's build() method. Execution and file read are enabled
// for the duration of the call and restored afterwards.
func (s *Shipfile) Build(ctx RunContext) error {
	cleanup, err := s.enableBuildSandbox()
	if err != nil {
		return err
	}
	defer cleanup()
	if s.HasBuild {
		if err := s.CallMethod(nil, "build", ctx.overrides()...); err != nil {
			return fmt.Errorf("build failed: %w", err)
		}
	}
	return nil
}

// TestResult holds the outcome of a single @test function.
type TestResult struct {
	Name        string
	Description string
	Passed      bool
	Error       string
}

// RunTests executes pre_test, all @test functions, and post_test in order.
// Both execution and file read/write are enabled for the duration of the call.
func (s *Shipfile) RunTests(ctx RunContext) ([]TestResult, error) {
	if s.interp == nil {
		return nil, fmt.Errorf("shipfile interpreter is not initialized")
	}
	sb := s.interp.Sandbox()
	prevExec := sb.ExecutionEnabled()
	prevFileRead := sb.FileReadEnabled()
	sb.SetExecution(true)
	sb.SetFileRead(true)
	sb.SetEnv(map[string]string{shipmod.RunDepthEnv: currentRunDepth()})
	defer func() {
		sb.SetExecution(prevExec)
		sb.SetFileRead(prevFileRead)
	}()

	inst := s.sfInst
	if inst == nil && s.sfClass != nil {
		var err error
		inst, err = py.Call(s.sfClass, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("instantiate Shipfile: %w", err)
		}
		s.sfInst = inst
	}

	ctxObj, err := s.NewContext(ctx.Version, ctx.overrides()...)
	if err != nil {
		return nil, err
	}
	if inst != nil && ctxObj != nil {
		_, _ = py.SetAttrString(inst, "ctx", ctxObj)
	}

	if s.HasPreTest && inst != nil {
		if fn, ok := findMethodInType(s.sfClass, inst, "pre_test"); ok {
			if err := callMethod(fn, inst); err != nil {
				return nil, fmt.Errorf("pre_test failed: %w", err)
			}
		}
	}

	type testEntry struct {
		name        string
		description string
		fn          py.Object
	}

	var tests []testEntry
	if typ, ok := s.sfClass.(*py.Type); ok {
		collectTests := func(dict py.StringDict) {
			for _, val := range dict {
				fn := unwrapCallable(val)
				nameObj, err := py.GetAttrString(fn, "__test_name")
				if err != nil {
					continue
				}
				name := ""
				if ns, ok := nameObj.(py.String); ok {
					name = string(ns)
				}
				description := ""
				if descObj, err := py.GetAttrString(fn, "__test_description"); err == nil {
					if ds, ok := descObj.(py.String); ok {
						description = string(ds)
					}
				}
				tests = append(tests, testEntry{name: name, description: description, fn: fn})
			}
		}
		collectTests(typ.Dict)
		for _, base := range typ.Mro {
			b, ok := base.(*py.Type)
			if !ok || b == typ || isTargetClass(b, "Tester") || isTargetClass(b, "Patcher") || isTargetClass(b, "Builder") || isTargetClass(b, "Shipfile") || b.Name == "object" {
				continue
			}
			collectTests(b.Dict)
		}
	}

	var results []TestResult
	for _, te := range tests {
		result := TestResult{Name: te.name, Description: te.description}
		if callErr := callMethod(te.fn, inst); callErr != nil {
			result.Passed = false
			result.Error = fmt.Sprintf("%v", callErr)
		} else {
			result.Passed = true
		}
		results = append(results, result)
	}

	if s.HasPostTest && inst != nil {
		if fn, ok := findMethodInType(s.sfClass, inst, "post_test"); ok {
			if err := callMethod(fn, inst); err != nil {
				return results, fmt.Errorf("post_test failed: %w", err)
			}
		}
	}

	return results, nil
}
