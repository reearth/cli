---
name: reearth-visualizer-plugin
description: Build, edit, or debug Re:Earth Visualizer plugins — reearth.yml manifests, extension JavaScript for the WebAssembly (QuickJS) side, iframe UI (HTML/CSS/JS), inspector property schemas, postMessage between the two sides, and code that runs as-is in the Plugin Playground. Use whenever the user mentions a Re:Earth plugin, Visualizer plugin, reearth.yml, Plugin Playground, widget / infoboxBlock / storyBlock extensions, or the `reearth.*` plugin API (reearth.ui, reearth.extension, reearth.layers, reearth.camera, reearth.timeline, reearth.data, reearth.viewer, reearth.modal, reearth.popup), or asks to show custom UI or data on a Re:Earth map. Do not use for the Re:Earth CMS API, the Re:Earth Visualizer server/backend, or plain CesiumJS apps outside Re:Earth.
---

# Re:Earth Visualizer Plugin Development

This skill lists patterns and pitfalls that were **run and confirmed in the Plugin Playground or in installed Visualizer**. Where the two behave differently, both are stated. It does not copy the API reference. For any method, option, or type not shown here, read the docs page instead of guessing, and say so if you could not confirm something.

## Docs

Base: `https://docs.reearth.io/en/developer/plugin/` (Japanese: replace `/en/` with `/ja/`)

| Topic | Page |
| --- | --- |
| How the two sides work (WebAssembly vs iframe) | `overview/how-the-plugin-system-works/` |
| Files and `reearth.yml` | `overview/plugin-structure/` |
| Widget vs InfoboxBlock vs StoryBlock | `overview/extension-types/` |
| Plugin Playground | `get-started/run-in-plugin-playground/` |
| Local development (template, DEV_PLUGIN) | `get-started/set-up-a-local-development-environment/` |
| Guide: display UI | `guides/display-ui/` |
| Guide: inspector settings | `guides/accept-user-configurable-properties/` |
| Guide: messaging between UI and plugin | `guides/communicate-between-a-plugin-and-reearth/` |
| Guide: add a layer | `guides/add-a-layer/` |
| Guide: infobox for a layer | `guides/configure-an-infobox-for-a-layer/` |
| Guide: basemap | `guides/change-the-basemap/` |
| Guide: timeline | `guides/play-the-timeline/` |
| Guide: large datasets | `guides/optimize-and-render-large-datasets/` |
| API reference index | `api-reference/` |
| `reearth.ui` / `extension` / `layers` / `camera` / `timeline` / `data` / `viewer` / `modal` / `popup` / `sketch` | `api-reference/<name>/` |
| Events (`on`, `off`, `once`) | `api-reference/events/` |
| Examples (Playground presets) | `examples/` |
| FAQ | `support/faq/` |

Other references:

- Manifest schema (field types, `ui` options, extension types): `https://github.com/reearth/reearth-visualizer/blob/main/server/schemas/plugin_manifest.json`
- Plugin Playground (no login): `https://visualizer.reearth.io/plugin-playground`
- Official plugins: `https://github.com/reearth-plugins`

## How a plugin runs

A plugin is one `reearth.yml` plus one JavaScript file per extension. Each extension's JS runs on the **WebAssembly side** (QuickJS). Any UI it shows runs in a sandboxed **iframe**.

| | WebAssembly side (extension JS) | iframe side (HTML from `reearth.ui.show`) |
| --- | --- | --- |
| Plugin API (`reearth.*`) | ✅ | ❌ `reearth` is undefined |
| `document`, `window`, DOM | ❌ undefined | ✅ |
| `fetch` | ❌ undefined | ✅ but only to servers that allow cross-origin requests |
| `setTimeout` / `setInterval` | ❌ undefined | ✅ |
| `localStorage` | ❌ undefined | ❌ reading it throws (sandboxed iframe) |
| Clipboard (`navigator.clipboard.writeText`) | ❌ | ❌ blocked by permissions policy, even on a user click |
| Talk to the other side | `reearth.ui.postMessage(msg)` / `reearth.extension.on("message", fn)` | `parent.postMessage(msg, "*")` / `window.addEventListener("message", fn)` |

**Rule of thumb:** the extension JS reads and changes the scene through `reearth.*`. The iframe renders UI, does network requests, and owns any timers. Data moves between them as messages.

## Files

```
reearth.yml
my-widget.js      # filename must equal the extension id
```

```yaml
id: my-plugin
name: My Plugin
version: 1.0.0
extensions:
  - id: my-widget
    type: widget        # or infoboxBlock, storyBlock
    name: My Widget
```

- Each extension `id` must match its JS filename exactly (`my-widget` → `my-widget.js`). With a mismatch the extension silently does not run: no UI, no error in the console.
- Use letters, digits, and `-` in IDs.
- **Don't use `reearth` as the plugin id.** Installing a plugin with `id: reearth` failed with `invalid manifest: cannot build system manifest`.

## Core pattern: UI + two-way messaging

```javascript
// my-widget.js — WebAssembly side
reearth.ui.show(`
  <style>
    html, body { margin: 0; }
    #root { padding: 12px; font-family: sans-serif; color: #111; background: #fff; border-radius: 8px; }
  </style>
  <div id="root">
    <button id="go">Do something</button>
    <p id="out"></p>
  </div>
  <script>
    document.getElementById("go").addEventListener("click", () => {
      parent.postMessage({ action: "go" }, "*");   // "*" is required
    });
    window.addEventListener("message", (e) => {
      if (e.data?.action === "result") {
        document.getElementById("out").textContent = e.data.payload;
      }
    });
  </script>
`, { width: 260, height: 100 });

reearth.extension.on("message", (msg) => {
  if (msg?.action === "go") {
    reearth.ui.postMessage({ action: "result", payload: "Hello from the plugin" });
  }
});
```

- Put an `action` (or `type`) field on every message and check it on receipt.
- Messages are serialized. Send plain data only: strings, numbers, booleans, plain objects and arrays. In testing:
  - A `Date` sent with `reearth.ui.postMessage` arrived in the iframe as an ISO **string**. Send times as epoch milliseconds.
  - A `Uint8Array` or `ArrayBuffer` sent to the iframe arrived as a plain object (`{"0":1,"1":2,"2":3}`).
  - A `Uint8Array` sent from the iframe arrived in the plugin as `undefined`.
  - Binary data must be converted first, e.g. to a base64 string. `btoa` exists only in the iframe, not in the extension JS.
- The iframe background is **transparent** by default and text is black. Always set a background and text color, or the UI can be unreadable on the map.
- **Width: set it explicitly** with `reearth.ui.show(html, { width, height })` or `reearth.ui.resize(width, height)` (both tested with both values). Without it the iframe was 300px wide whatever the content, and wider content was cut off.
- **Height followed the content automatically** in testing, even when an explicit `height` was passed (`height: 100` with 400px of content gave a 400px iframe).
- A plain `</script>` inside the template literal works, and so does `@import` inside `<style>`.
- **Backslashes inside the `reearth.ui.show` template literal are consumed by the extension JS** before the iframe sees them. A regex like `/\//g` in the iframe script arrives as `///g`, which breaks the **whole** iframe script silently: no listeners, no button handlers, no console error. Double every backslash that the iframe code needs:

```javascript
reearth.ui.show(`<script>
  "2025/08/01".replace(/\\//g, "-");   // iframe receives /\//g → "2025-08-01"
</script>`);
```

## Inspector settings (properties)

Define a schema in `reearth.yml`. Visualizer builds the settings UI from it.

```yaml
extensions:
  - id: my-widget
    type: widget
    name: My Widget
    schema:
      groups:
        - id: settings          # any id; it becomes the key in `property`
          title: Settings
          fields:
            - id: label
              type: string
              title: Label       # always include: required by the manifest schema; without it the input has no label
              defaultValue: Hello
            - id: mode
              type: string
              title: Mode
              ui: selection
              choices:
                - { key: a, label: Option A }
                - { key: b, label: Option B }
            - id: color
              type: string
              title: Color
              ui: color
```

```javascript
const p = reearth.extension.widget?.property?.settings ?? {};
const label = p.label ?? "Hello";   // repeat the YAML defaultValue here
const mode  = p.mode  ?? "a";
```

- The path is `property.<groupId>.<fieldId>`. Renaming an id in the YAML means renaming it in the JS.
- **`defaultValue` does not reach the plugin in installed Visualizer.** Until the user saves a value, the field is `undefined` in `property`, before and after a page reload, even though the Inspector shows the default in the input. The Playground behaves differently: it does return defaults. So always read with `?.` and fall back with `??` to the same value as the YAML `defaultValue`. Code that relies on the Playground's defaults breaks once installed.
- For **storyBlock** and **infoboxBlock** extensions, read `reearth.extension.block?.property` (same `<groupId>.<fieldId>` shape). `reearth.extension.widget` is `undefined` there.
- In an infoboxBlock, `reearth.extension.block?.layer` is the layer of the feature the user clicked, e.g. `block.layer.id`.
- **No event fires when a setting changes**, in the Playground or in installed Visualizer (`reearth.extension.on("update")`, `reearth.extension.on("extensionMessage")` and `reearth.ui.on("update")` were all registered and never called).
  - Installed Visualizer: reading `reearth.extension.widget.property` again returns the new value right away, with no reload. To react to changes, re-read it, e.g. on a timer started in the iframe that messages the plugin.
  - Playground: re-reading `property` keeps returning the old value. The new value is picked up only when the code is run again with **Run**.
- **Don't use `type: latlng`.** In the Playground it returned `[null, <lng>]` after both values were entered. Use two `number` fields.
- For other field types and `ui` options, check the manifest schema.

## Showing, hiding, closing the UI

- `reearth.ui.show(false)` throws a `TypeError`. `show` always needs an HTML string.
- `reearth.ui.show(html, { visible: false })` creates the UI hidden.
- `reearth.ui.close()` removes the UI.
- To toggle while running, send a message and switch CSS in the iframe:

```javascript
reearth.ui.postMessage({ action: "visible", show: false });
// iframe: if (e.data?.action === "visible") root.style.display = e.data.show ? "block" : "none";
```

## Fetching external data

Fetch in the iframe. If the plugin needs the result (for example to add a layer), post it back:

```javascript
// iframe
fetch(url)
  .then((r) => { if (!r.ok) throw new Error("HTTP " + r.status); return r.json(); })
  .then((data) => parent.postMessage({ action: "data", data }, "*"))
  .catch((err) => showError(err.message));
```

- The server must allow cross-origin requests (`Access-Control-Allow-Origin: *`). If it doesn't, `fetch` fails with `TypeError: Failed to fetch`. Tell the user the data source blocks browser access instead of retrying.
- A GeoJSON layer can also load straight from a URL with `data: { type: "geojson", url }`, no fetch needed (see below).

## Layers and camera

```javascript
// Points: `marker` appearance
const pointsId = reearth.layers.add({
  type: "simple",
  data: { type: "geojson", url: "https://example.com/points.geojson" },   // or value: { ...FeatureCollection }
  marker: { style: "point", pointColor: "#ff0000", pointSize: 12 },
});

// Lines (LineString / MultiLineString): `polyline` appearance
const linesId = reearth.layers.add({
  type: "simple",
  data: { type: "geojson", url: "https://example.com/lines.geojson" },
  polyline: { strokeColor: "#ff0000", strokeWidth: 3 },
});

reearth.camera.flyTo({ lat: 35.68, lng: 139.76, height: 200000 }, { duration: 2 });

reearth.layers.override(linesId, { visible: false });   // hide; { visible: true } to show again
reearth.layers.delete(pointsId);
```

- **A layer with no appearance key is not drawn.** The layer is added (you get an id) but nothing appears on the map. Always include one. `marker` for points and `polyline` for lines were tested and drawn with the given color and width. For polygons and other appearance keys, check `api-reference/layers/`.
- `layers.add` returns the new layer's id. Keep it for `override` and `delete`.
- `layers.override(id, { visible: false })` / `{ visible: true }` hides and shows a plugin-added layer.
- A returned id does **not** mean the layer is valid. `data: { type: "not-a-type" }` still returned an id. Only an empty object (`layers.add({})`) returned `undefined`.
- `layers.delete` on a layer that is part of the scene (not added by the plugin) had no effect. The layer was still there.
- `override` applies asynchronously. The change appears in `reearth.layers.overridden` a moment later, and `reearth.layers.findById(id)` keeps returning the **original** definition.
- After `delete`, `findById(id)` returns nothing.
- For other data types, appearance keys, and camera options, see `api-reference/layers/` and `api-reference/camera/`.

## Timeline

```javascript
reearth.timeline.setTime({ start: "2025-08-01T00:00:00Z", stop: "2025-08-31T23:00:00Z", current: "2025-08-01T00:00:00Z" });
reearth.timeline.setSpeed(3600);   // measured: 1 real second = 1 simulated hour
reearth.timeline.play();

const sendTick = (d) => reearth.ui.postMessage({ action: "tick", time: d.getTime() });
reearth.timeline.on("tick", sendTick);   // callback receives a Date
```

- `setTime` applies asynchronously. Reading `startTime` or `currentTime` right after it still returns the old values. The new range shows up from the next `tick`, so drive the UI from `tick`.
- `tick` fired about 60 times per second in testing. Keep the handler light.
- Format times in the iframe with an explicit time zone. Don't rely on the viewer's local zone:

```javascript
new Intl.DateTimeFormat("en-GB", { timeZone: "Asia/Tokyo", dateStyle: "short", timeStyle: "short" }).format(new Date(ms));
// 2025-08-01T00:00Z → "01/08/2025, 09:00"
```

## Saving data

`localStorage` is unavailable on both sides. Use `reearth.data.clientStorage` from the extension JS. `setAsync` and `getAsync` return promises:

```javascript
reearth.data.clientStorage.setAsync("theme", "dark")
  .then(() => reearth.data.clientStorage.getAsync("theme"))
  .then((theme) => reearth.ui.postMessage({ action: "theme", theme }));
```

**Do not use top-level `await`** in extension JS. It is a syntax error (`expecting ';'`), and the whole extension fails to run. Use `.then()` or an `async` function.

## Debugging

- `console.log` in the extension JS prints to the browser console.
- Syntax errors in the extension JS stop the whole extension. They appear only in the browser console as `plugin error {message: …, lineNumber: …}`, with nothing in the UI.

## Plugin Playground

- Edit `reearth.yml` and the JS, then press **Run**. The first run can take several seconds to show the widget.
- The Extension Settings panel shows the schema of the extension whose JS file is selected.
- infoboxBlock extensions need **Enable Infobox** ticked under Settings. Then clicking a feature on the map opens the infobox, and each block appears inside it. With Enable Infobox unticked, clicking a feature opens no infobox, so the blocks never run.
- storyBlock extensions run when **Show Story Panel** is ticked.
- The preset examples under `examples/` are working references. Copy their patterns when in doubt.
- Presets load a shared demo stylesheet: `<style>@import url("https://reearth.github.io/visualizer-plugin-sample-data/public/css/preset-ui.css");</style>`.
- **Export plugin** downloads `<playground plugin id>.zip` with `reearth.yml` and the JS files inside a folder. That `.zip` installed as-is in a Visualizer project, and the widget worked after being added from the Widgets tab.
- Test in installed Visualizer before handing over anything that uses settings. Defaults and setting changes behave differently there (see "Inspector settings").

## Packaging and installing

- A `.zip` with `reearth.yml` and the JS files inside one folder installs. The widget then appears under **Add Widget** in the Widgets tab.
- **Keep the `.zip` under 10 MB** (the documented limit). A 9.4 MB `.zip` installed. A 10.1 MB one was rejected with `zip open error: zip: not a valid zip file`. The message doesn't mention size, so check the file size first when you see it.
- A `.zip` that also contained a `.png` and a `.css` installed, and the widget ran. For images and CSS, the docs say to embed them in the JS as strings or load them from a public URL.

## Checking and packing with the Re:Earth CLI

If the `reearth` CLI is installed, it can check a plugin and build the `.zip`. Find the commands with `reearth search "validate a plugin"` and `reearth search "zip a plugin for upload"`, then read their `--help`.

- The validate command checks `reearth.yml` the way Visualizer reads an upload. In testing it reported a schema field without `title` (`… .title is required`) and the plugin id `reearth` (`"reearth" is reserved`). Visualizer itself installs the first without complaint (the input just has no label) and rejects the second with an unclear message, so run validate before every upload.
- The pack command validates first and does not pack a plugin with problems. Over the size limit it fails with `the package would be <size>; Visualizer accepts up to 10.0 MB`.

## Before handing code to the user

- [ ] Every extension `id` matches a `.js` filename.
- [ ] Every schema field has `id`, `type`, and `title`. The JS reads `property.<group>.<field>` with `?.`, and each `??` fallback equals the field's YAML `defaultValue`.
- [ ] No `fetch`, `document`, `window`, `setTimeout`, or top-level `await` in the extension JS. No `reearth.*` in the iframe script.
- [ ] iframe → plugin messages use `parent.postMessage(msg, "*")`, and the plugin listens with `reearth.extension.on("message")`.
- [ ] Messages carry plain data (no `Date` objects).
- [ ] Every backslash in the iframe code inside the `reearth.ui.show` template literal is doubled (`\\`).
- [ ] Every `layers.add` has an appearance key (`marker` for points, `polyline` for lines).
- [ ] The UI sets a background, a text color, and an explicit width.
- [ ] If the `reearth` CLI is installed, its validate command passes.
- [ ] Anything used that isn't in this file was checked against its `api-reference/` page. Tell the user which parts you could not confirm.
