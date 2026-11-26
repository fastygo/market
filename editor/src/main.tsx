import { render } from "solid-js/web";
import { EditorY } from "@ui8kit/editory-solid";
import type { LongreadLabels } from "editory";
import "@ui8kit/editory-solid/style.css";

type Props = {
  labels: LongreadLabels;
  title: string;
  csrf: string;
  version: number;
  saveUrl: string;
  uploadUrl: string;
  conflict?: string;
  uploadFailed?: string;
  uploading?: string;
};

function readProps(): Props | null {
  const el = document.getElementById("editory-props");
  if (!el?.textContent) return null;
  try {
    return JSON.parse(el.textContent) as Props;
  } catch {
    return null;
  }
}

function start() {
  const mount = document.getElementById("editory-root");
  const area = document.getElementById("description") as HTMLTextAreaElement | null;
  const props = readProps();
  if (!mount || !area || !props?.labels || !props.saveUrl) return;
  area.hidden = true;

  let markdown = area.value;
  let version = props.version;
  let timer = 0;
  let insert: ((s: string) => void) | null = null;
  const status = document.createElement("p");
  status.setAttribute("role", "status");
  status.className = "text-sm text-muted-foreground";
  mount.parentElement?.insertBefore(status, mount);

  const setStatus = (msg: string) => {
    status.textContent = msg;
  };

  const save = async (body: string) => {
    const res = await fetch(props.saveUrl, {
      method: "PATCH",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": props.csrf,
        "If-Match": `"v${version}"`,
      },
      body: JSON.stringify({ description: body, version }),
    });
    if (res.status === 409) {
      setStatus(props.conflict || "conflict");
      return;
    }
    if (!res.ok) {
      setStatus(props.uploadFailed || "error");
      return;
    }
    const data = (await res.json()) as { version?: number };
    if (typeof data.version === "number") version = data.version;
    const hidden = document.querySelector('input[name="version"]') as HTMLInputElement | null;
    if (hidden) hidden.value = String(version);
  };

  const schedule = (body: string) => {
    window.clearTimeout(timer);
    timer = window.setTimeout(() => {
      void save(body);
    }, 2000);
  };

  const onMarkdown = (value: string) => {
    markdown = value;
    area.value = value;
    schedule(value);
  };

  const onFile = async (file: File) => {
    setStatus(props.uploading || "");
    const data = new FormData();
    data.set("file", file);
    const res = await fetch(props.uploadUrl, {
      method: "POST",
      credentials: "same-origin",
      headers: { "X-CSRF-Token": props.csrf },
      body: data,
    });
    if (!res.ok) {
      setStatus(props.uploadFailed || "error");
      return;
    }
    const out = (await res.json()) as { url?: string };
    if (!out.url) {
      setStatus(props.uploadFailed || "error");
      return;
    }
    const snippet = `![](${out.url})`;
    if (insert) insert(snippet);
    else onMarkdown(markdown.trim() ? `${markdown.trim()}\n\n${snippet}\n` : `${snippet}\n`);
    setStatus("");
  };

  const bar = document.createElement("div");
  bar.className = "flex flex-wrap items-center gap-2";
  const upload = document.createElement("input");
  upload.type = "file";
  upload.accept = "image/jpeg,image/png,image/webp,image/gif";
  upload.className = "sr-only";
  upload.id = "editory-upload";
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "inline-flex items-center rounded-md border border-border bg-background px-3 py-2 text-sm";
  btn.textContent = props.labels.insertImage;
  btn.addEventListener("click", () => upload.click());
  upload.addEventListener("change", () => {
    const file = upload.files?.[0];
    upload.value = "";
    if (file) void onFile(file);
  });
  bar.append(btn, upload);
  mount.parentElement?.insertBefore(bar, mount);

  render(
    () => (
      <EditorY
        markdown={markdown}
        labels={props.labels}
        title={props.title || ""}
        onMarkdown={onMarkdown}
        onInsertReady={(fn) => {
          insert = fn;
        }}
      />
    ),
    mount,
  );
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", start);
} else {
  start();
}
