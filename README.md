# 📦 Packly

**The frontend toolchain for server-rendered apps. One binary. One `.ini`. No Node.js. No magic.**

**Packly** is an ultra-lightweight asset builder and frontend package manager written in Go. It downloads your frontend dependencies (Bootstrap, FontAwesome, Leaflet, etc., as CDN files or ZIP archives), compiles your Sass/SCSS with the official `dart-sass`, and copies everything to `public/build/`, **without Node.js, npm, or Webpack/Vite**.

Packly is built on a strong philosophy: **No hidden magic.** It downloads what you ask for and compiles your Sass. It does not bundle, it does not parse your code, it does not tree-shake your JavaScript, and it will never auto-inject tags into your HTML. You are the developer, you stay in control.

Like Symfony AssetMapper or importmap-rails, but framework-agnostic and shipped as a single Go binary.

**[Download the latest release](https://github.com/michelware/packly/releases/latest)**

---

## 💾 Install

Packly ships as **5 standalone binaries**. Download the one matching your platform from the [latest release](https://github.com/michelware/packly/releases/latest):

| Platform | File |
| --- | --- |
| 🐧 Linux x64 | `packly-linux-amd64` |
| 🐧 Linux ARM64 | `packly-linux-arm64` |
| 🍎 macOS Intel | `packly-macos-amd64` |
| 🍎 macOS Apple Silicon (M1/M2/M3…) | `packly-macos-arm64` |
| 🪟 Windows x64 | `packly-windows-amd64.exe` |

**Linux / macOS**: make it executable and put it in your `PATH` (replace the file name with yours):

```bash
chmod +x packly-linux-amd64
sudo mv packly-linux-amd64 /usr/local/bin/packly
```

**Windows**: rename `packly-windows-amd64.exe` to `packly.exe` and place it in a folder listed in your `PATH` (or directly at your project root).

These are the same 5 platforms for which Packly can fetch a `dart-sass` binary automatically.

---

## ⚡ Why Packly? (The "Plus")

### 🚫 Zero Node.js / npm

No `node_modules` weighing 500MB, no `package.json`, no lock files, no JavaScript tooling complexity. Packly is a single, fast Go binary.

### 🌐 Direct CDN & ZIP Downloads

Declare URLs from jsDelivr, unpkg, or direct GitHub releases, and Packly vendors them locally into your project. Need a minified file? Point to the `.min.js`. Need an ES Module? Point to the `.mjs` or `+esm` build.

When a ZIP archive is extracted, its top-level folder (e.g. `bootstrap-5.3.3/`) is removed, and folders that are useless in production (`docs/`, `site/`, `test/`, `tests/`, `demo/`, `samples/`, `.github`) are skipped.

### 🎨 Built-in Auto-Sass

Just provide the version number. Packly automatically detects your OS and architecture, downloads the matching `dart-sass` binary once (into a local `.sass/` folder), and compiles your stylesheets. Output is minified and without source maps by default, and both can be changed in `packly.ini`.

### 🌍 Any Backend

PHP with or without a framework, Symfony, legacy apps, Go, Django, Flask… Packly only produces static files in a folder, so it works with anything that can serve them.

### 🧙‍♂️ No Hidden Magic

Tired of tools generating `<script type="importmap">` behind your back? Packly respects your intelligence. It prepares your files in the `public/` directory and leaves the HTML integration entirely up to you. You write your own `<script>` tags, your own import maps, and you keep total control over your frontend architecture.

### 🔍 Readable

The whole tool is a single Go file you can read and audit yourself.

---

## 🆚 Comparison

| | npm + Vite / Webpack | Symfony AssetMapper | importmap-rails | **Packly** |
| --- | --- | --- | --- | --- |
| **Runtime** | Node.js | PHP, Symfony | Ruby, Rails | **Single Go binary** |
| **Framework** | Any | Symfony only | Rails only | **Any** |
| **Setup** | Config files, scripts | Built into Symfony | Built into Rails | **1 small `.ini` file** |
| **Sass** | Loader or plugin | With SassBundle | Separate gem | **Built in (download & compile)** |
| **Import map** | Not used | Generated | Generated | **You write it** |
| **Bundling** | Yes | No | No | **No, by design** |

---

## 🚀 Quick Start (KISS)

### 1. Configure (`packly.ini`)

Create a `packly.ini` at the root of your project. If it does not exist, the first `packly install` or `packly build` generates a default one. It acts as both your configuration and your dependency list:

```ini
[build]
# Directory where your source assets (Sass, images, custom JS) are located (default: assets)
source = assets
# Target directory where the built assets will be published (copied to <target>/build/, default: public)
target = public
# Comma-separated list of paths (relative to 'source') to exclude from the build target
# Note: Packly automatically excludes .scss/.sass source files and hidden files (starting with ".")
ignore = vendor/bootstrap

[dependencies]
# PRINCIPLE:
# KEY   = The name of the directory created under `<source>/vendor/<KEY>/`
# VALUE = Comma-separated list of URLs to download.
#         - If ZIP file: downloaded and automatically unzipped inside `<source>/vendor/<KEY>/`
#         - If single file: downloaded directly as `<source>/vendor/<KEY>/<filename>`
jquery = https://cdn.jsdelivr.net/npm/jquery@3.7.1/dist/jquery.min.js
leaflet = https://unpkg.com/leaflet@1.9.4/dist/leaflet.js, https://unpkg.com/leaflet@1.9.4/dist/leaflet.css
bootstrap = https://github.com/twbs/bootstrap/archive/refs/tags/v5.3.3.zip

[sass]
# Version of dart-sass used for compilation (Packly fetches the right binary for your OS)
version = 1.79.2
# Sass entrypoint files (relative to 'source') compiled to CSS during build
entrypoints = styles/app.scss, styles/admin.scss
# CSS output style: "compressed" (default, minified) or "expanded" (readable, handy in development)
style = compressed
# Generate .css.map source maps next to the CSS files (default: false)
sourcemap = false
```

### 2. Run Commands

* **Install dependencies** (downloads/unzips assets under `assets/vendor/<key>/`):

```bash
packly install
```

Already installed URLs are skipped; change a URL in `packly.ini` and it is downloaded again on the next install. Add `--force` to reinstall all dependencies:

```bash
packly install --force
```

* **Build & Compile** (copies raw assets to `public/build/` and compiles Sass entrypoints):

```bash
packly build
```

> ⚠️ `public/build/` is deleted and recreated on every build. Never put files there by hand; put them in `assets/`.

* **Watch Mode** (watches the `assets/` directory and rebuilds on every change):

```bash
packly build --watch
```

> The `vendor/` folder is not watched: run `packly install` then `packly build` again after changing dependencies.

### 3. Git

Packly adds a `.gitignore` to the folders it manages, so `assets/vendor/` and `.sass/` stay out of your repository. You will probably want to ignore `public/build/` yourself too.

---

## 📖 Concrete Example: Modern ES6 Modules Workflow

To help you visualize how Packly interacts with native browser standards, here is a complete, end-to-end example using a modern ES6 module. We will use `canvas-confetti` as it is visual and perfect for demonstrating native ES modules.

### 1. Configuration (`packly.ini`)

Define your dependencies by pointing directly to an ES Module (`.mjs`) build on a CDN.

```ini
[build]
source = assets
target = public

[dependencies]
# We fetch the ES module version directly
confetti = https://cdn.jsdelivr.net/npm/canvas-confetti@1.9.2/dist/confetti.module.mjs

[sass]
version = 1.79.2
entrypoints = styles/app.scss
```

### 2. Directory Workflow (Before / After)

Here is exactly how Packly processes and maps your physical files:

**Phase A: Running `packly install`**
Packly reads your `.ini` file and downloads the module locally into your source directory:

* `assets/vendor/confetti/confetti.module.mjs`

**Phase B: Your Custom Assets**
You create your own files in your source folder:

* `assets/js/app.js`
* `assets/styles/app.scss`

**Phase C: Running `packly build`**
Packly performs a clean, straightforward mirror copy of your `assets/` folder over to `public/build/` (while omitting raw `.scss`/`.sass` files). Your final production-ready directory structure exposed to the web becomes:

* `public/build/vendor/confetti/confetti.module.mjs`
* `public/build/js/app.js`
* `public/build/styles/app.css` *(compiled automatically by the Sass engine)*

---

### 3. Your JavaScript Code (`assets/js/app.js`)

You write standard, modern JavaScript using **bare module specifiers**. You don't need to specify complex, relative physical paths in your application code; the native browser handles that using your import map.

```javascript
// assets/js/app.js
import confetti from 'confetti';

document.addEventListener('DOMContentLoaded', () => {
    const btn = document.getElementById('btn-party');

    if (btn) {
        btn.addEventListener('click', () => {
            confetti({
                particleCount: 100,
                spread: 70,
                origin: { y: 0.6 }
            });
            console.log("Native ES6 Modules running successfully!");
        });
    }
});
```

---

### 4. Native HTML Integration (`index.html`)

This is where you assume full responsibility and control over your frontend. Packly handles the mechanics (downloading and putting assets in the right directory), and leaves the `<script>` and `<link>` declarations explicitly up to you.

```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>Packly ES6 Native Example</title>

    <link rel="stylesheet" href="/build/styles/app.css">

    <script type="importmap">
    {
      "imports": {
        "confetti": "/build/vendor/confetti/confetti.module.mjs"
      }
    }
    </script>
</head>
<body>

    <button id="btn-party">Throw Confetti!</button>

    <script type="module" src="/build/js/app.js"></script>

</body>
</html>
```

---

## 🚫 What Packly Will Never Do

Bundle your JavaScript, tree-shake, parse your code, inject tags into your HTML, load plugins, or require a JS config file.

---

## 📄 Licence

Packly is distributed under the Packly Source-Available License. See the [LICENCE](LICENCE) file.
