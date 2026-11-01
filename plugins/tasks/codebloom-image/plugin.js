// CodeBloom's verified image-only transports. A model's name does not grant
// reference, editing, quality or resolution controls that its route did not test.
const IMAGE_TRANSPORTS = {
  "gpt-image-2.5-sunburst": "images_generations",
  "gpt-image-2.5-flare": "images_generations",
  "gpt-image-2k": "images_generations",
  "grok-imagine-image-2.0": "chat_completions",
  "grok-imagine-image": "chat_completions",
  "gemini-3.1-flash-image-preview": "chat_completions",
  "nano-banana-2": "chat_completions",
  "gemini-nano-banana-2.1": "chat_completions",
  "nano-banana-pro": "chat_completions",
  "gemini-3-pro-image-preview": "chat_completions",
};
const MODELS = Object.keys(IMAGE_TRANSPORTS).map((id) => "ys-" + id);

export const meta = {
  apiVersion: 1,
  key: "codebloom-image",
  name: "CodeBloom Images",
  version: "1.0.0",
  icon: "text:CB",
  author: { name: "Yunshu" },
  baseUrl: "https://codebloom.club",
  description: { en: "Verified CodeBloom synchronous image transports", zh: "已核验的 CodeBloom 同步图片协议" },
  models: MODELS,
  fetchMode: "per_task",
  usageSchema: {
    image_count: { type: "number", unit: "count", description: { en: "Image request unit price", zh: "图片请求单价" } },
  },
  usageExamples: [{ label: "One image request", facts: { image_count: 1 } }],
  protocols: [{ name: "openai_image", models: MODELS, retainResult: true }],
};

function object(value) {
  return value && typeof value === "object" && !Array.isArray(value);
}
function modelFor(ctx) {
  const id = ctx.model;
  if (typeof id !== "string" || !MODELS.includes(id)) throw new Error("Image model is not in the CodeBloom catalog");
  const upstream = id.slice("ys-".length);
  if (ctx.upstreamModel !== undefined && ctx.upstreamModel !== id && ctx.upstreamModel !== upstream)
    throw new Error("The image model mapping differs from the declared route");
  return { id, upstream, transport: IMAGE_TRANSPORTS[upstream] };
}
function normalize(ctx) {
  const model = modelFor(ctx);
  const body = ctx.requestBody;
  if (!object(body)) throw new Error("A JSON image request is required");
  for (const key of Object.keys(body))
    if (!["model", "prompt", "n", "count", "size", "stream", "response_format"].includes(key)) throw new Error("Unsupported image parameter: " + key);
  if (body.model !== undefined && body.model !== model.id) throw new Error("Image model differs from the declared route");
  if (typeof body.prompt !== "string" || !body.prompt.trim() || Array.from(body.prompt).length > 16000)
    throw new Error("A nonempty image prompt of at most 16000 characters is required");
  for (const key of ["n", "count"]) if (body[key] !== undefined && body[key] !== 1) throw new Error("Only one image request is supported");
  if (body.stream !== undefined && body.stream !== false) throw new Error("Image streaming is not supported");
  if (body.response_format !== undefined && !["url", "b64_json"].includes(body.response_format)) throw new Error("Unsupported image response format");
  if (model.transport === "images_generations") {
    if (body.size !== undefined && body.size !== "1024x1024") throw new Error("Only the verified 1024x1024 image size is supported");
  } else if (body.size !== undefined) throw new Error("This image transport has no verified size control");
  return { model, prompt: body.prompt };
}

export function buildSubmitRequest(ctx) {
  const normalized = normalize(ctx);
  if (typeof ctx.publicTaskId !== "string" || !ctx.publicTaskId) throw new Error("A durable gateway task identity is required");
  const root = typeof ctx.baseUrl === "string" ? ctx.baseUrl.replace(/\/+$/, "") : "";
  if (!root) throw new Error("Image upstream is not configured");
  const images = normalized.model.transport === "images_generations";
  return {
    url: root + (images ? "/v1/images/generations" : "/v1/chat/completions"),
    method: "POST",
    headers: { Authorization: "Bearer " + ctx.apiKey, "Content-Type": "application/json" },
    body: images
      ? { model: normalized.model.upstream, prompt: normalized.prompt, size: "1024x1024", n: 1 }
      : { model: normalized.model.upstream, messages: [{ role: "user", content: normalized.prompt }], stream: false },
    action: "text_to_image",
  };
}

// Only explicit image carriers or a URL occupying a complete line are image
// evidence. A prose hyperlink cannot turn an ordinary answer into an image.
function publicImageURL(value) {
  if (typeof value !== "string" || value.length > 16384 || value !== value.trim()) return "";
  return /^https:\/\/[A-Za-z0-9][A-Za-z0-9.-]*(?::443)?(?:\/[^\s\\<>"']*)?$/.test(value) ? value : "";
}
function uniqueURLs(values) {
  const result = [];
  for (const value of values) {
    const url = publicImageURL(value);
    if (url && !result.includes(url)) result.push(url);
  }
  return result;
}
function messageImages(message) {
  if (!object(message)) return [];
  const markdown = [];
  const bare = [];
  if (typeof message.content === "string") {
    const pattern = /!\[[^\]\r\n]*\]\((https:\/\/[^\s)]+)(?:\s+"[^"\r\n]*")?\)/g;
    let match;
    while ((match = pattern.exec(message.content))) markdown.push(match[1]);
    for (const line of message.content.split(/\r?\n/)) if (publicImageURL(line.trim())) bare.push(line.trim());
  }
  const structured = [];
  for (const image of Array.isArray(message.images) ? message.images : []) {
    if (typeof image === "string") structured.push(image);
    else if (object(image)) structured.push(image.url || (object(image.image_url) ? image.image_url.url : image.image_url));
  }
  // Bare mirror links supplement a rendered image in some CodeBloom replies;
  // they are fallback evidence, not additional billable output images.
  const explicit = uniqueURLs(markdown.concat(structured));
  return explicit.length ? explicit : uniqueURLs(bare);
}
function outputURL(ctx) {
  if (object(ctx.state) && publicImageURL(ctx.state.output_url)) return ctx.state.output_url;
  let data = object(ctx.data) ? ctx.data : {};
  if (object(data.data) && data.data.task_id && object(data.data.data)) data = data.data.data;
  return publicImageURL(data.output_url);
}

export function parseSubmitResponse(ctx, response) {
  const normalized = normalize(ctx);
  const body = response.body;
  if (!Number.isInteger(response.statusCode) || response.statusCode < 200 || response.statusCode >= 300 || !object(body) || body.error)
    throw new Error("Image upstream did not return a verified result");
  if (body.model !== undefined && body.model !== normalized.model.upstream) throw new Error("Image upstream returned a different model");
  let urls = [];
  if (normalized.model.transport === "images_generations") {
    urls = uniqueURLs((Array.isArray(body.data) ? body.data : []).filter(object).map((item) => item.url));
  } else {
    const choices = Array.isArray(body.choices) ? body.choices : [];
    for (const choice of choices) {
      if (!object(choice) || choice.finish_reason !== "stop") continue;
      urls = urls.concat(messageImages(choice.message));
    }
    urls = uniqueURLs(urls);
  }
  if (!urls.length) throw new Error("Image upstream did not return a verified image");
  if (typeof ctx.publicTaskId !== "string" || !ctx.publicTaskId) throw new Error("A durable gateway task identity is required");
  const data = { output_url: urls[0] };
  return {
    taskId: ctx.publicTaskId,
    taskData: data,
    state: data,
    immediate: { status: "SUCCESS", progress: "100%", url: urls[0], usageFacts: { image_count: 1 } },
  };
}
export function extractUsage(ctx) {
  normalize(ctx);
  return { image_count: 1 };
}
export function extractUsageOnComplete() {
  return {};
}
export function buildQueryRequest() {
  throw new Error("This synchronous image upstream has no verified retrieval endpoint");
}
export function parseTaskResult() {
  return { status: "UNKNOWN", reason: "This image upstream has no verified retrieval endpoint" };
}
export function listArtifacts(task) {
  if (task.status !== "SUCCESS" || !outputURL(task)) return [];
  return [{ key: "image-1", type: "image" }];
}
export function buildContentRequest(ctx) {
  const url = outputURL(ctx);
  const method = (ctx.clientRequest || {}).method || "GET";
  if (!url || ctx.artifactKey !== "image-1" || !["GET", "HEAD"].includes(method)) throw new Error("artifact_not_found");
  return { url, method, credentialless: true };
}
export const protocols = {
  openai_image: {
    decodeRequest(ctx) {
      if (ctx.operation !== "generate") throw new Error("Image editing is not supported by this route");
      if (!ctx.body || ctx.body.kind !== "json" || !object(ctx.body.value)) throw new Error("A JSON image request is required");
      const req = ctx.body.value;
      const body = Object.assign({}, req, { model: ctx.model });
      if (req.model !== undefined && req.model !== ctx.model) throw new Error("Image model differs from the declared route");
      normalize({ model: ctx.model, requestBody: body });
      return { kind: "submit", model: ctx.model, action: "text_to_image", requestBody: body };
    },
    render(ctx, task) {
      const artifact = object(ctx.artifacts) && object(ctx.artifacts["image-1"]) ? ctx.artifacts["image-1"] : null;
      const url = artifact && typeof artifact.url === "string" ? artifact.url : outputURL(task);
      if (task.status !== "SUCCESS" || !url) throw new Error("Verified image result is unavailable");
      return { created: task.created_at || 0, data: [{ url }] };
    },
  },
};
