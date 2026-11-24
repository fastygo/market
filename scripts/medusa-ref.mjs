#!/usr/bin/env node
// Extract a compact, machine-readable reference of the Medusa v2 data model
// (models, module links, provider interfaces, workflow ids, HTTP routes)
// from a local checkout. No dependencies. Usage:
//   node scripts/medusa-ref.mjs <medusa-checkout> <out-dir>
// Medusa is MIT licensed except Enterprise Edition materials listed in its
// ENTERPRISE-LICENSE.md (RBAC, SSO/OIDC); those paths are skipped.
import fs from "node:fs"
import path from "node:path"
import { execSync } from "node:child_process"

const [root, out] = process.argv.slice(2)
if (!root || !out) { console.error("usage: medusa-ref.mjs <medusa> <out>"); process.exit(2) }
const EE = [/\/rbac\//, /rbac/i, /auth-oidc/, /\/policies\//]
const isEE = (p) => EE.some((r) => r.test(p))
const rd = (p) => fs.readFileSync(p, "utf8")
function walk(dir, pred, acc = []) {
  if (!fs.existsSync(dir)) return acc
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name)
    if (isEE(p)) continue
    if (e.isDirectory()) walk(p, pred, acc)
    else if (pred(p)) acc.push(p)
  }
  return acc
}
const stripComments = (s) => s.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|[^:])\/\/.*$/gm, "$1")

// enums: `export enum X { A = "a" }` and `export const X = { A: "a" }`
const enums = {}
for (const f of walk(path.join(root, "packages"), (p) => p.endsWith(".ts") && !p.endsWith(".d.ts"))) {
  const s = stripComments(rd(f))
  for (const m of s.matchAll(/export\s+(?:declare\s+)?(?:const\s+)?enum\s+(\w+)\s*\{([\s\S]*?)\}/g)) {
    enums[m[1]] = [...m[2].matchAll(/=\s*["'`]([^"'`]+)["'`]/g)].map((x) => x[1])
  }
}

function matchClose(s, i, open, close) { // s[i] === open
  let d = 0, q = null
  for (let j = i; j < s.length; j++) {
    const c = s[j]
    if (q) { if (c === "\\") j++; else if (c === q) q = null; continue }
    if (c === '"' || c === "'" || c === "`") { q = c; continue }
    if (c === open) d++
    else if (c === close && --d === 0) return j
  }
  return -1
}
function splitTop(body) {
  const parts = []; let d = 0, q = null, cur = ""
  for (let j = 0; j < body.length; j++) {
    const c = body[j]
    if (q) { cur += c; if (c === "\\") { cur += body[++j] } else if (c === q) q = null; continue }
    if (c === '"' || c === "'" || c === "`") { q = c; cur += c; continue }
    if ("([{<".includes(c)) d++
    if (")]}>".includes(c) && !(c === ">" && body[j - 1] === "=")) d--
    if (c === "," && d === 0) { parts.push(cur); cur = ""; continue }
    cur += c
  }
  if (cur.trim()) parts.push(cur)
  return parts.map((p) => p.trim()).filter(Boolean)
}
function parseField(name, expr) {
  const e = expr.replace(/\s+/g, " ")
  const m = e.match(/model\s*\.\s*(\w+)\s*/)
  if (!m) return { name, type: "unknown", raw: e.slice(0, 120) }
  const f = { name, type: m[1] }
  let k = m.index + m[0].length
  if (e[k] === "<") { // skip generic type arguments, ignoring "=>"
    let d = 0
    for (; k < e.length; k++) {
      if (e[k] === "<") d++
      else if (e[k] === ">" && e[k - 1] !== "=" && --d === 0) { k++; break }
    }
  }
  const argStart = e.indexOf("(", k)
  const argEnd = matchClose(e, argStart, "(", ")")
  const args = e.slice(argStart + 1, argEnd).trim()
  if (["belongsTo", "hasMany", "hasOne", "manyToMany", "hasOneWithFK"].includes(f.type)) {
    const t = args.match(/=>\s*(\w+)/); if (t) f.target = t[1]
    const mb = args.match(/mappedBy:\s*["'](\w+)["']/); if (mb) f.mappedBy = mb[1]
    const pt = args.match(/pivotTable:\s*["'](\w+)["']/); if (pt) f.pivotTable = pt[1]
  } else if (f.type === "enum") {
    if (args.startsWith("[")) f.values = [...args.matchAll(/["'`]([^"'`]+)["'`]/g)].map((x) => x[1])
    else { f.enum = args.replace(/^.*\./, ""); if (enums[f.enum]) f.values = enums[f.enum] }
  } else if (f.type === "id") {
    const p = args.match(/prefix:\s*["'](\w+)["']/); if (p) f.prefix = p[1]
  }
  const mods = e.slice(argEnd + 1)
  for (const mod of ["nullable", "primaryKey", "searchable", "unique", "index", "translatable", "computed"]) {
    if (new RegExp(`\\.\\s*${mod}\\s*\\(`).test(mods)) f[mod] = true
  }
  const d = mods.match(/\.\s*default\s*\(([^)]*)\)/); if (d) f.default = d[1].trim().replace(/^.*\./, "")
  return f
}

const models = {}
const modDir = path.join(root, "packages/modules")
for (const mod of fs.readdirSync(modDir).sort()) {
  const files = walk(path.join(modDir, mod, "src/models"), (p) => p.endsWith(".ts") && !p.endsWith("index.ts"))
  for (const f of files.sort()) {
    const s = stripComments(rd(f))
    for (const m of s.matchAll(/\.define\s*\(/g)) {
      const open = s.indexOf("(", m.index)
      const close = matchClose(s, open, "(", ")")
      const args = splitTop(s.slice(open + 1, close))
      if (args.length < 2) continue
      const nm = args[0].match(/name:\s*["'](\w+)["']/) || args[0].match(/^["'](\w+)["']/)
      const tbl = args[0].match(/tableName:\s*["'](\w+)["']/)
      const body = args[1].trim()
      if (!body.startsWith("{")) continue
      const fields = splitTop(body.slice(1, -1)).map((p) => {
        const k = p.match(/^["']?(\w+)["']?\s*:\s*([\s\S]*)$/)
        return k ? parseField(k[1], k[2]) : null
      }).filter(Boolean)
      const name = nm ? nm[1] : path.basename(f, ".ts")
      ;(models[mod] ??= {})[name] = { ...(tbl ? { table: tbl[1] } : {}), file: path.relative(root, f), fields }
    }
  }
}

// module links
const links = []
for (const f of walk(path.join(root, "packages/modules/link-modules/src/definitions"), (p) => p.endsWith(".ts") && !p.endsWith("index.ts"))) {
  const s = stripComments(rd(f))
  const tbl = s.match(/tableName:\s*["'](\w+)["']/)
  const ri = s.indexOf("relationships:")
  const rb = s.indexOf("[", ri)
  const relBlock = ri < 0 ? "" : s.slice(rb, matchClose(s, rb, "[", "]") + 1)
  const rel = [...relBlock.matchAll(/serviceName:\s*Modules\.(\w+),\s*entity:\s*["'](\w+)["'][\s\S]*?foreignKey:\s*["'](\w+)["']/g)]
    .map((r) => ({ module: r[1].toLowerCase(), entity: r[2], foreignKey: r[3] }))
  links.push({ file: path.basename(f, ".ts"), table: tbl ? tbl[1] : null, relationships: rel })
}

// provider interfaces
const methodsOf = (file, re) => {
  if (!fs.existsSync(file)) return []
  const s = stripComments(rd(file))
  return [...s.matchAll(re)].map((m) => m[1])
}
const providers = {
  AbstractPaymentProvider: methodsOf(path.join(root, "packages/core/utils/src/payment/abstract-payment-provider.ts"), /abstract\s+(\w+)\s*\(/g),
  ITaxProvider: (() => {
    const f = path.join(root, "packages/core/types/src/tax/provider.ts")
    const s = stripComments(rd(f)); const i = s.indexOf("interface ITaxProvider")
    const b = s.indexOf("{", i); return [...s.slice(b, matchClose(s, b, "{", "}")).matchAll(/^\s*(\w+)\s*\(/gm)].map((m) => m[1])
  })(),
  payment_enums: Object.fromEntries(["PaymentSessionStatus", "PaymentCollectionStatus", "PaymentActions"].filter((k) => enums[k]).map((k) => [k, enums[k]])),
}

// workflows
const workflows = {}
for (const f of walk(path.join(root, "packages/core/core-flows/src"), (p) => /\/workflows\/[^/]+\.ts$/.test(p))) {
  const s = rd(f)
  for (const m of s.matchAll(/export const (\w+WorkflowId)\s*=\s*["']([\w.-]+)["']/g)) {
    const area = path.relative(path.join(root, "packages/core/core-flows/src"), f).split(path.sep)[0]
    ;(workflows[area] ??= []).push(m[2])
  }
}
for (const k of Object.keys(workflows)) workflows[k].sort()

// HTTP routes
const routes = { store: [], admin: [] }
for (const scope of ["store", "admin"]) {
  const base = path.join(root, "packages/medusa/src/api", scope)
  for (const f of walk(base, (p) => p.endsWith("route.ts"))) {
    const s = rd(f)
    const verbs = [...s.matchAll(/export const (GET|POST|PUT|PATCH|DELETE)\b/g)].map((m) => m[1])
    const p = "/" + scope + "/" + path.relative(base, path.dirname(f)).split(path.sep).join("/")
    routes[scope].push(`${verbs.join(",")} ${p.replace(/\/$/, "")}`)
  }
  routes[scope].sort()
}

let commit = "unknown", tag = "unknown"
try { commit = execSync(`git -C ${root} rev-parse HEAD`).toString().trim() } catch {}
try { tag = execSync(`git -C ${root} describe --tags --exact-match`).toString().trim() } catch {}
const meta = { source: "https://github.com/medusajs/medusa", tag, commit, license: "MIT (Enterprise Edition paths excluded)", generator: "scripts/medusa-ref.mjs" }

fs.mkdirSync(out, { recursive: true })
const w = (n, v) => fs.writeFileSync(path.join(out, n), JSON.stringify({ meta, ...v }, null, 1) + "\n")
w("models.json", { models })
w("links.json", { links })
w("providers.json", { providers })
w("workflows.json", { workflows })
w("routes.json", { routes })
const count = Object.values(models).reduce((n, m) => n + Object.keys(m).length, 0)
console.log(`modules ${Object.keys(models).length}, models ${count}, links ${links.length}, workflows ${Object.values(workflows).flat().length}, routes ${routes.store.length + routes.admin.length}`)
