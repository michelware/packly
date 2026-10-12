// Packly is a small asset bundler and dependency manager for web projects.
//
// It reads its configuration from a "packly.ini" file in the current
// directory and offers two commands:
//
//	packly install [--force]   download the third-party files listed in
//	                           [dependencies] into <source>/vendor
//	packly build [--watch]     copy the source folder to <target>/build,
//	                           compile Sass entrypoints, and optionally
//	                           rebuild whenever a file changes
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/md5"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/ini.v1"
)

// defaultIni is written to packly.ini when no configuration file exists yet.
//
// Supported keys:
//   - [build] source:  folder holding the assets to bundle (default "assets")
//   - [build] target:  folder receiving the output; files land in <target>/build
//   - [build] ignore:  optional comma-separated paths, relative to source, to skip
//   - [dependencies]:  one key per package, whose value is a comma-separated
//     list of URLs (.zip archives are extracted, other files are copied as-is)
//   - [sass] version:     dart-sass release to download
//   - [sass] entrypoints: comma-separated .scss/.sass files, relative to source
//   - [sass] style:       "compressed" (default) or "expanded"
//   - [sass] sourcemap:   true to emit .css.map files (default false)
const defaultIni = `[build]
source = assets
target = public

[dependencies]

[sass]
version = 1.79.2
entrypoints =
style = compressed
sourcemap = false
`

// sassConfig holds the [sass] section of packly.ini.
type sassConfig struct {
	// Version is the dart-sass release to download, e.g. "1.79.2".
	Version string
	// Entrypoints are the Sass files to compile, relative to the source folder.
	Entrypoints []string
	// Style is the CSS output style passed to --style: "compressed" or "expanded".
	Style string
	// SourceMap enables source map generation; when false, --no-source-map is passed.
	SourceMap bool
}

// v is the Packly version.
var v = "1.0.0"

// httpClient is shared by every download so that a stalled server cannot
// block Packly forever.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// loadConfig reads packly.ini, creating it from defaultIni first if it does
// not exist, and returns, in order:
//   - the source folder,
//   - the destination folder (<target>/build),
//   - the ignore patterns from [build] ignore,
//   - the dependencies, mapping each package name to its list of URLs,
//   - the Sass settings.
//
// The program exits if the file cannot be parsed or contains an invalid
// Sass style.
func loadConfig() (string, string, []string, map[string][]string, sassConfig) {
	var src, dest string
	var ignores []string
	var deps map[string][]string
	var sass sassConfig

	filename := "packly.ini"
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		_ = os.WriteFile(filename, []byte(defaultIni), 0644)
		fmt.Println("Created default packly.ini configuration file.")
	}

	cfg, err := ini.Load(filename)
	if err != nil {
		log.Fatalf("Failed to load packly.ini: %v", err)
	}

	buildSec := cfg.Section("build")
	src = buildSec.Key("source").MustString("assets")
	target := buildSec.Key("target").MustString("public")
	dest = filepath.Join(target, "build")
	ignorePatterns := buildSec.Key("ignore").MustString("")
	ignores = []string{}
	if ignorePatterns != "" {
		for _, pattern := range strings.Split(ignorePatterns, ",") {
			ignores = append(ignores, strings.TrimSpace(pattern))
		}
	}

	deps = make(map[string][]string)
	for _, name := range cfg.Section("dependencies").KeyStrings() {
		urlStr := cfg.Section("dependencies").Key(name).String()
		parts := strings.Split(urlStr, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		deps[name] = parts
	}

	sassSec := cfg.Section("sass")
	sass.Version = sassSec.Key("version").String()
	if sassSec.HasKey("entrypoints") {
		eps := sassSec.Key("entrypoints").String()
		for _, ep := range strings.Split(eps, ",") {
			// Skip blanks so an empty "entrypoints =" means no Sass at all.
			if ep = strings.TrimSpace(ep); ep != "" {
				sass.Entrypoints = append(sass.Entrypoints, ep)
			}
		}
	}
	sass.Style = strings.ToLower(sassSec.Key("style").MustString("compressed"))
	if sass.Style != "compressed" && sass.Style != "expanded" {
		log.Fatalf("Invalid [sass] style '%s' in packly.ini: expected 'compressed' or 'expanded'", sass.Style)
	}
	sass.SourceMap = sassSec.Key("sourcemap").MustBool(false)

	return src, dest, ignores, deps, sass
}

// copyFile copies the file at src to dst, creating dst's parent folders when
// needed and overwriting dst if it already exists.
func copyFile(src, dst string) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// md5sum returns the hex-encoded MD5 hash of s. It is only used to build
// short, filesystem-safe marker names from URLs, not for security.
func md5sum(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// downloadFile fetches urlStr into a new temporary file and returns its path.
// Any status other than 200 OK is an error. The caller is responsible for
// removing the temporary file.
func downloadFile(urlStr string) (string, error) {
	resp, err := httpClient.Get(urlStr)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "packly-*")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	_, err = io.Copy(tmp, resp.Body)
	return tmp.Name(), err
}

// safeJoin joins an archive entry name to the destination directory and
// refuses any entry that would escape it (e.g. "../../etc/passwd").
func safeJoin(destination, name string) (string, error) {
	outPath := filepath.Join(destination, name)
	cleanDest := filepath.Clean(destination) + string(os.PathSeparator)
	if !strings.HasPrefix(outPath+string(os.PathSeparator), cleanDest) {
		return "", fmt.Errorf("illegal path in archive: %s", name)
	}
	return outPath, nil
}

// unzip extracts the zip archive at source into destination.
//
// When stripRoot is true and the archive's first entry is a folder (as in
// GitHub archives, e.g. "project-main/"), that folder prefix is removed so
// its contents land directly in destination. Entries matched by isIgnored
// (docs, tests, demos...) are skipped.
func unzip(source, destination string, stripRoot bool) error {
	r, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer r.Close()

	var root string
	if stripRoot && len(r.File) > 0 && r.File[0].FileInfo().IsDir() {
		root = r.File[0].Name
	}

	for _, f := range r.File {
		p := f.Name
		if root != "" && strings.HasPrefix(p, root) {
			p = strings.TrimPrefix(p, root)
		}
		if p == "" || isIgnored(p) {
			continue
		}
		outPath, err := safeJoin(destination, p)
		if err != nil {
			return err
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(outPath, 0755)
			continue
		}

		os.MkdirAll(filepath.Dir(outPath), 0755)
		out, err := os.Create(outPath)
		if err != nil {
			return err
		}
		in, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		_, err = io.Copy(out, in)
		out.Close()
		in.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// untar extracts the gzip-compressed tar archive at source into destination,
// keeping each file's permissions (needed for executables such as sass).
func untar(source, destination string) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		outPath, err := safeJoin(destination, header.Name)
		if err != nil {
			return err
		}
		info := header.FileInfo()

		if info.IsDir() {
			os.MkdirAll(outPath, 0755)
			continue
		}

		os.MkdirAll(filepath.Dir(outPath), 0755)
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		_, err = io.Copy(out, tarReader)
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// isIgnored reports whether an archive entry should be left out when
// installing a dependency: documentation, tests, demos, samples and CI files
// are not needed to serve the assets.
func isIgnored(p string) bool {
	p = strings.ToLower(filepath.ToSlash(p))
	ignores := []string{"/docs/", "/site/", "/tests/", "/test/", "/demo/", "/samples/", ".github", ".travis.yml"}
	for _, ig := range ignores {
		if strings.Contains("/"+p+"/", ig) {
			return true
		}
	}
	return false
}

// installDependencies downloads every dependency into <source>/vendor/<name>.
//
// Each URL that installs successfully leaves a hidden marker file named after
// the URL's hash, so later runs skip it. Changing a URL in packly.ini
// therefore triggers a new download. With force, each package folder is
// deleted first so everything is reinstalled. Any failure stops the program.
func installDependencies(source string, deps map[string][]string, force bool) {
	vendor := filepath.Join(source, "vendor")
	_ = os.MkdirAll(vendor, 0755)

	// Keep downloaded files out of version control.
	gitIgnore := filepath.Join(vendor, ".gitignore")
	if _, err := os.Stat(gitIgnore); os.IsNotExist(err) {
		_ = os.WriteFile(gitIgnore, []byte("*"), 0644)
	}

	fmt.Println("Installing dependencies...")

	if force {
		fmt.Println("Force mode enabled. Reinstalling all dependencies...")
	}

	// Sort dependency names for deterministic and organized output
	var sortedNames []string
	for name := range deps {
		sortedNames = append(sortedNames, name)
	}
	sort.Strings(sortedNames)

	for i, name := range sortedNames {
		urlList := deps[name]
		target := filepath.Join(vendor, name)

		if force {
			if _, err := os.Stat(target); err == nil {
				_ = os.RemoveAll(target)
			}
		}

		fmt.Printf("\n📦 [%d/%d] %s\n", i+1, len(sortedNames), name)

		for _, urlStr := range urlList {
			marker := filepath.Join(target, fmt.Sprintf(".%s_installed", md5sum(urlStr)))

			if _, err := os.Stat(marker); err == nil {
				fmt.Printf("   ✓ %s (already installed)\n", urlStr)
				continue
			}

			fmt.Printf("   → Downloading %s...\n", urlStr)
			tmp, err := downloadFile(urlStr)
			if err != nil {
				log.Fatalf("Failed to download '%s': %v", urlStr, err)
			}

			if _, err := os.Stat(target); err != nil {
				if err := os.MkdirAll(target, 0755); err != nil {
					os.Remove(tmp)
					log.Fatalf("Failed to create directory '%s': %v", target, err)
				}
			}

			// Archives are extracted; any other file is copied under its
			// URL file name, without the query string.
			if strings.HasSuffix(strings.ToLower(urlStr), ".zip") {
				if err := unzip(tmp, target, true); err != nil {
					os.Remove(tmp)
					log.Fatalf("Failed to unzip '%s': %v", urlStr, err)
				}
			} else {
				filename := path.Base(urlStr)
				if idx := strings.Index(filename, "?"); idx != -1 {
					filename = filename[:idx]
				}

				if err := copyFile(tmp, filepath.Join(target, filename)); err != nil {
					os.Remove(tmp)
					log.Fatalf("Failed to copy file for '%s': %v", urlStr, err)
				}
			}
			_ = os.WriteFile(marker, []byte(urlStr), 0644)
			_ = os.Remove(tmp)
			fmt.Printf("   ✓ Successfully installed.\n")
		}
	}
	fmt.Println("\nAll dependencies processed.")
}

// sassArchiveName returns the dart-sass release asset matching the current
// OS and architecture, e.g. "dart-sass-1.79.2-macos-arm64.tar.gz".
func sassArchiveName(version string) (string, error) {
	osNames := map[string]string{"linux": "linux", "darwin": "macos", "windows": "windows"}
	archNames := map[string]string{"amd64": "x64", "arm64": "arm64"}

	osName, okOS := osNames[runtime.GOOS]
	archName, okArch := archNames[runtime.GOARCH]
	if !okOS || !okArch {
		return "", fmt.Errorf("unsupported platform for dart-sass: %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	extension := ".tar.gz"
	if runtime.GOOS == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("dart-sass-%s-%s-%s%s", version, osName, archName, extension), nil
}

// compileSass compiles each Sass entrypoint from src into a .css file at the
// same relative path in dest, using the output style and source map setting
// from sass (minified and without source map by default).
//
// The dart-sass binary is downloaded on first use into ./.sass (ignored by
// git) and reused afterwards. Missing entrypoints only print a warning; a
// compilation error stops the program and shows the Sass output.
func compileSass(src string, dest string, sass sassConfig) {
	if len(sass.Entrypoints) == 0 {
		return
	}

	binaryName := "sass"
	if runtime.GOOS == "windows" {
		binaryName = "sass.bat"
	}
	binaryPath := filepath.Join(Getcwd(), ".sass", "dart-sass", binaryName)

	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		fmt.Println("Sass binary not found. Downloading...")

		archiveName, err := sassArchiveName(sass.Version)
		if err != nil {
			log.Fatalf("%v", err)
		}
		binaryURL := fmt.Sprintf("https://github.com/sass/dart-sass/releases/download/%s/%s", sass.Version, archiveName)

		tmp, err := downloadFile(binaryURL)
		if err != nil {
			log.Fatalf("Failed to download Sass: %v", err)
		}
		defer os.Remove(tmp)

		target := filepath.Join(Getcwd(), ".sass")
		_ = os.MkdirAll(target, 0755)

		gitIgnore := filepath.Join(target, ".gitignore")
		if _, err := os.Stat(gitIgnore); os.IsNotExist(err) {
			_ = os.WriteFile(gitIgnore, []byte("*"), 0644)
		}

		if strings.HasSuffix(strings.ToLower(binaryURL), ".zip") {
			if err := unzip(tmp, target, false); err != nil {
				log.Fatalf("Failed to unzip Sass: %v", err)
			}
		} else if strings.HasSuffix(strings.ToLower(binaryURL), ".tar.gz") {
			if err := untar(tmp, target); err != nil {
				log.Fatalf("Failed to untar Sass: %v", err)
			}
		} else {
			log.Fatalf("Unsupported archive format for Sass: %s", binaryURL)
		}

		// Make sure the extracted script is executable on Unix systems.
		if runtime.GOOS != "windows" {
			_ = os.Chmod(binaryPath, 0755)
		}
		fmt.Println("Sass binary installed successfully.")
	}

	args := []string{"--style=" + sass.Style}
	if !sass.SourceMap {
		args = append(args, "--no-source-map")
	}

	for _, ep := range sass.Entrypoints {
		inputPath := filepath.Join(src, ep)
		if _, err := os.Stat(inputPath); os.IsNotExist(err) {
			fmt.Printf("Warning: Sass entrypoint not found: %s\n", inputPath)
			continue
		}

		outputPath := filepath.Join(dest, strings.TrimSuffix(ep, filepath.Ext(ep))+".css")
		_ = os.MkdirAll(filepath.Dir(outputPath), 0755)
		fmt.Printf("Compiling Sass: %s -> %s\n", inputPath, outputPath)

		cmd := exec.Command(binaryPath, append(args, inputPath, outputPath)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Fatalf("Sass compilation failed for %s: %v\nOutput:\n%s", inputPath, err, string(output))
		}
	}
}

// build produces a fresh copy of src in dest, then compiles the Sass
// entrypoints into it.
//
// dest is emptied first so deleted source files do not linger. The copy
// skips Sass sources (they are compiled instead), hidden files and folders
// (names starting with "."), and any path listed in ignores. The program
// exits if the build fails.
func build(src string, dest string, ignores []string, sass sassConfig) {
	fmt.Printf("Building assets from '%s' to '%s'...\n", src, dest)
	_ = os.RemoveAll(dest)

	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".scss" || ext == ".sass" {
			return nil
		}

		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}

		if strings.HasPrefix(filepath.Base(path), ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// An ignore entry matches the path itself or anything inside it.
		// Paths are compared with forward slashes so packly.ini works the
		// same on every OS.
		for _, ignore := range ignores {
			normRel := filepath.ToSlash(rel)
			normIgnore := strings.TrimSuffix(filepath.ToSlash(ignore), "/")
			if normRel == normIgnore || strings.HasPrefix(normRel, normIgnore+"/") {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		outPath := filepath.Join(dest, rel)
		if info.IsDir() {
			return os.MkdirAll(outPath, info.Mode())
		}
		return copyFile(path, outPath)
	})
	if err != nil {
		log.Fatalf("Build failed: %v", err)
	}

	compileSass(src, dest, sass)
	fmt.Println("Build completed successfully.")
}

// watch monitors src and rebuilds into dest whenever a file changes. It runs
// until the program is stopped.
//
// fsnotify does not watch subfolders recursively, so each folder is added
// one by one, and newly created folders are added as they appear. The vendor
// folder and the ignored paths are not watched.
func watch(src, dest string, ignores []string, sass sassConfig) {
	watcher, _ := fsnotify.NewWatcher()
	defer watcher.Close()

	// watchFolders adds root and all of its subfolders to the watcher.
	watchFolders := func(root string) {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if d.IsDir() && d.Name() == "vendor" {
				return filepath.SkipDir
			}
			if err != nil || !d.IsDir() {
				return nil
			}
			for _, ig := range ignores {
				if strings.Contains(p, ig) {
					return nil
				}
			}
			watcher.Add(p)
			return nil
		})
	}

	watchFolders(src)
	fmt.Println("Watching for changes in", src)

	// An editor save often fires several events in a row. Each event resets
	// a 300 ms timer, so only one build runs once things settle. The mutex
	// keeps two builds from ever running at the same time.
	var timer *time.Timer
	var mu sync.Mutex

	for event := range watcher.Events {
		// Ignore editor backup files ("file~") and hidden files.
		if strings.HasSuffix(event.Name, "~") || strings.HasPrefix(filepath.Base(event.Name), ".") {
			continue
		}

		if stat, err := os.Stat(event.Name); err == nil && stat.IsDir() {
			watchFolders(event.Name)
		}

		if timer != nil {
			timer.Stop()
		}

		timer = time.AfterFunc(300*time.Millisecond, func() {
			mu.Lock()
			defer mu.Unlock()
			build(src, dest, ignores, sass)
		})
	}
}

// printUsage prints the list of available commands.
func printUsage() {
	fmt.Println("Usage: packly <command> [options]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  install              Install all dependencies listed in the configuration file.")
	fmt.Println("  build [--watch]      Build assets based on the configuration. Use --watch to monitor changes.")
}

// Getcwd returns the current working directory, or an empty string if it
// cannot be determined.
func Getcwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// main loads the configuration, then dispatches to the requested command.
func main() {
	fmt.Println("📦 Packly - Asset Bundler & Dependency Manager for Web")
	fmt.Println("---------------------------------------------------------")

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]
	src, dest, ignores, deps, sass := loadConfig()

	osName := runtime.GOOS
	archName := runtime.GOARCH

	fmt.Printf("OS: %s\n", osName)
	fmt.Printf("Arch: %s\n", archName)

	switch command {
	case "install":
		installCmd := flag.NewFlagSet("install", flag.ExitOnError)
		forceFlag := installCmd.Bool("force", false, "force reinstall of dependencies")
		if len(os.Args) > 2 {
			installCmd.Parse(os.Args[2:])
		}
		installDependencies(src, deps, *forceFlag)
	case "build":
		buildCmd := flag.NewFlagSet("build", flag.ExitOnError)
		watchFlag := buildCmd.Bool("watch", false, "watch files for changes")
		if len(os.Args) > 2 {
			buildCmd.Parse(os.Args[2:])
		}

		build(src, dest, ignores, sass)
		if *watchFlag {
			watch(src, dest, ignores, sass)
		}
	default:
		fmt.Printf("Error: Unknown command '%s'\n\n", command)
		printUsage()
		os.Exit(1)
	}
}