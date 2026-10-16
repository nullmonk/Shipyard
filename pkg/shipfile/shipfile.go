package shipfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-python/gpython/py"
	"shipyard/pkg/shipyard"
)

// Shipfile represents the parsed configuration of a shipfile.py.
type Shipfile struct {
	Path            string
	Dir             string
	Name            string
	Url             string
	Urls            []string
	Branch          string
	VersionTags     string
	IgnoredVersions []string
	PackageName     string
	Repository      string
	BaseImage       string
	Setup           string
	Build           string
	Artifacts       string
	ArtifactDir     string
	Variables            map[string]string
	VariableDescriptions map[string]string
	Patches              string
	HasCodePatches    bool
	HasPatchFiles     bool
	ImplementsPatcher bool
	ImplementsBuilder bool
	HasPackageName    bool
	HasPrePatch       bool
	HasPostPatch      bool
	HasSetup          bool
	HasBuild          bool
}

// IsBuild returns true if this shipfile implements the Builder subclass.
func (s *Shipfile) IsBuild() bool {
	return s.ImplementsBuilder
}

// IsPatch returns true if this shipfile implements the Patcher subclass.
func (s *Shipfile) IsPatch() bool {
	return s.ImplementsPatcher
}

// IsGeneric returns true if this shipfile builds itself in a plain container
// rather than using a distro package manager.
func (s *Shipfile) IsGeneric() bool {
	return s.IsBuild()
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
func Load(path string, interp *shipyard.Interpreter) (*Shipfile, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	mod, err := interp.RunShipfile(absPath)
	if err != nil {
		py.TracebackDump(err)
		return nil, fmt.Errorf("run shipfile %s: %w", absPath, err)
	}

	sfClass, ok := mod.Globals["Shipfile"]
	if !ok {
		return nil, fmt.Errorf("no Shipfile class found in %s", absPath)
	}

	sf := &Shipfile{
		Path:        absPath,
		Dir:         filepath.Dir(absPath),
		Name:        getAttrString(sfClass, "Name"),
		Url:         firstNonEmpty(getAttrString(sfClass, "Url"), getAttrString(sfClass, "URL"), getAttrString(sfClass, "url")),
		Urls:        firstNonEmptySlice(getAttrStringSlice(sfClass, "Urls"), getAttrStringSlice(sfClass, "URLs"), getAttrStringSlice(sfClass, "URLS"), getAttrStringSlice(sfClass, "urls")),
		Branch:      getAttrString(sfClass, "Branch"),
		VersionTags: getAttrString(sfClass, "VersionTags"),
		PackageName: firstNonEmpty(getAttrString(sfClass, "PackageName"), getAttrString(sfClass, "Package"), getAttrString(sfClass, "package_name")),
		Repository:  getAttrString(sfClass, "Repository"),
		BaseImage:   getAttrString(sfClass, "BaseImage"),
		Setup:       getAttrString(sfClass, "Setup"),
		Build:       getAttrString(sfClass, "Build"),
		Artifacts:            getAttrString(sfClass, "Artifacts"),
		ArtifactDir:          getAttrString(sfClass, "ArtifactDir"),
		Patches:              getAttrString(sfClass, "Patches"),
		Variables:            make(map[string]string),
		VariableDescriptions: make(map[string]string),
	}

	if sf.Patches == "" {
		sf.Patches = "patches/"
	}
	if sf.VersionTags == "" {
		sf.VersionTags = "*"
	}

	inst, _ := py.Call(sfClass, nil, nil)
	if typ, ok := sfClass.(*py.Type); ok {
		checkDict := func(dict py.StringDict) {
			for key, val := range dict {
				if _, ok := sf.Variables[key]; ok {
					continue
				}
				isVar := false
				if _, err := py.GetAttrString(val, "__is_variable__"); err == nil {
					isVar = true
				}
				if !isVar {
					if t := val.Type(); t != nil && t.Name == "Variable" {
						isVar = true
					}
				}
				if isVar {
					desc := ""
					if d, err := py.GetAttrString(val, "__variable_description__"); err == nil {
						if s, ok := d.(py.String); ok {
							desc = string(s)
						}
					}
					if desc == "" {
						if d, err := py.GetAttrString(val, "description"); err == nil {
							if s, ok := d.(py.String); ok {
								desc = string(s)
							}
						}
					}

					var valStr string
					if getValFn, err := py.GetAttrString(val, "get_value"); err == nil {
						var res py.Object
						var err error
						if inst != nil {
							res, err = py.Call(getValFn, py.Tuple{inst}, nil)
						} else {
							res, err = py.Call(getValFn, py.Tuple{}, nil)
						}
						if err == nil {
							if s, ok := res.(py.String); ok {
								valStr = string(s)
							}
						}
					} else if inst != nil {
						if attr, err := py.GetAttrString(inst, key); err == nil {
							if s, ok := attr.(py.String); ok {
								valStr = string(s)
							}
						}
					}

					sf.Variables[key] = valStr
					sf.VariableDescriptions[key] = desc
				}
			}

			if !sf.HasCodePatches {
				for _, val := range dict {
					fn := unwrapCallable(val)
					if _, err := py.GetAttrString(fn, "__patch_files"); err == nil {
						sf.HasCodePatches = true
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
			if _, ok := dict["setup"]; ok {
				sf.HasSetup = true
			}
			if _, ok := dict["build"]; ok {
				sf.HasBuild = true
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
			if isTargetClass(b, "Patcher") || isTargetClass(b, "Builder") || isTargetClass(b, "Shipfile") || b.Name == "object" {
				continue
			}
			checkDict(b.Dict)
		}
	}

	if sf.PackageName != "" {
		sf.HasPackageName = true
	}

	// If sf.PackageName is empty but package_name method exists, try invoking it on an instance
	if sf.PackageName == "" && sf.HasPackageName {
		if inst, err := py.Call(sfClass, nil, nil); err == nil {
			if meth, err := py.GetAttrString(inst, "package_name"); err == nil {
				if res, err := py.Call(meth, nil, nil); err == nil {
					if s, ok := res.(py.String); ok && string(s) != "" {
						sf.PackageName = string(s)
					}
				} else if res, err := py.Call(meth, py.Tuple{py.None}, nil); err == nil {
					if s, ok := res.(py.String); ok && string(s) != "" {
						sf.PackageName = string(s)
					}
				}
			}
		}
	}

	// Detect raw patch files in the patches directory
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

	// Detect subclass implementation
	sf.ImplementsPatcher = ImplementsSubclass(sfClass, "Patcher")
	sf.ImplementsBuilder = ImplementsSubclass(sfClass, "Builder")

	if inst != nil {
		if valMeth, err := py.GetAttrString(inst, "validate"); err == nil {
			if _, err := py.Call(valMeth, nil, nil); err != nil {
				py.TracebackDump(err)
				return nil, fmt.Errorf("shipfile %s validation error: %v", absPath, err)
			}
		}
	}

	return sf, nil
}

// LoadDefault creates an interpreter with default package root and parses the shipfile.
func LoadDefault(path string, pkgDir string) (*Shipfile, error) {
	if pkgDir == "" {
		pkgDir = "."
	}
	interp, err := shipyard.New(pkgDir)
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

