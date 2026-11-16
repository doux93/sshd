const fs = require("node:fs"),
  path = require("node:path"),
  vm = require("node:vm"),
  ts = require("typescript");
function fixture(storage = new Map()) {
  const loc = {
    href: "https://test.example/",
    origin: "https://test.example",
    protocol: "https:",
    host: "test.example",
    pathname: "/",
    search: "",
    assign() {},
    replace() {},
  };
  const ctx = vm.createContext({
    console,
    URL,
    location: loc,
    window: { location: loc, history: { replaceState() {} } },
    localStorage: {
      getItem: (k) => storage.get(k) ?? null,
      setItem: (k, v) => storage.set(k, v),
    },
    sessionStorage: { getItem: () => null, removeItem() {}, setItem() {} },
    fetch: async () => ({ ok: true, json: async () => [] }),
    setTimeout,
    clearTimeout,
    requestAnimationFrame: (fn) => fn(),
    confirm: () => true,
    prompt: () => null,
    navigator: { clipboard: { writeText: async () => {} } },
  });
  const cache = new Map();
  function load(file) {
    file = path.resolve(__dirname, "..", file);
    if (cache.has(file)) return cache.get(file).exports;
    const mod = { exports: {} };
    cache.set(file, mod);
    const out = ts.transpileModule(fs.readFileSync(file, "utf8"), {
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2022,
        jsx: ts.JsxEmit.ReactJSX,
      },
    }).outputText;
    const wrapped = vm.runInContext(
      `(function(require,module,exports){${out}\n})`,
      ctx,
    );
    wrapped(
      (name) => {
        if (!name.startsWith(".")) return require(name);
        const base = path.resolve(path.dirname(file), name);
        const target = [".ts", ".tsx", ".js"]
          .map((ext) => base + ext)
          .find((f) => fs.existsSync(f));
        if (!target) throw Error(name);
        return load(target);
      },
      mod,
      mod.exports,
    );
    return mod.exports;
  }
  return { load, storage };
}
module.exports = { fixture };
