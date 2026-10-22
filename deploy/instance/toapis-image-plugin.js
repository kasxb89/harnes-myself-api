export const meta = {
  apiVersion: 1,
  key: "toapis-image",
  name: "ToAPIs GPT-Image-2",
  icon: "text:TI",
  version: "1.2.1",
  author: { name: "Yunshu" },
  description: { en: "Async GPT-Image-2 generation", zh: "GPT-Image-2 异步生图" },
  baseUrl: "https://toapis.cn",
  models: ["yunshu-internal-gpt-image-2"],
  fetchMode: "per_task",
  usageSchema: {
    image_count: { type: "number", unit: "count", description: { en: "Image generation unit price", zh: "图片生成单价" } },
    resolution: {
      enum: ["1k", "2k", "4k"],
      description: { en: "Output image resolution", zh: "输出图片分辨率" },
      enumLabels: { "1k": { en: "1K", zh: "1K" }, "2k": { en: "2K", zh: "2K" }, "4k": { en: "4K", zh: "4K" } },
    },
  },
  usageExamples: [
    { label: "1K", facts: { image_count: 1, resolution: "1k" } },
    { label: "2K", facts: { image_count: 1, resolution: "2k" } },
    { label: "4K", facts: { image_count: 1, resolution: "4k" } },
  ],
  routes: [
    {
      method: "POST",
      path: "/toapis/v1/images/generations",
      type: "submit",
      models: ["yunshu-internal-gpt-image-2"],
      decode: "decodeSubmit",
      render: "renderSubmit",
    },
    { method: "GET", path: "/toapis/v1/images/generations/:task_id", type: "query", render: "renderTask" },
  ],
};

function record(value) {
  return value && typeof value === "object" && !Array.isArray(value);
}

function imageResolution(value) {
  if (value === undefined) return "1k";
  if (typeof value !== "string" || !/^(1k|2k|4k)$/i.test(value)) {
    throw new Error("resolution must be 1k, 2k or 4k");
  }
  return value.toLowerCase();
}

// ToAPIs uses size for aspect ratio, never a pixel count. Old desktop pixel
// presets were ignored by 1.2.0 and remain ignored; resolution owns the tariff.
const IMAGE_RATIOS = ["1:1", "3:2", "2:3", "4:3", "3:4", "5:4", "4:5", "16:9", "9:16", "2:1", "1:2", "21:9", "9:21"];
function imageAspectRatio(body) {
  let ratio;
  if (typeof body.size === "string" && body.size.includes(":")) {
    if (!IMAGE_RATIOS.includes(body.size)) throw new Error("size must be a supported image aspect ratio");
    ratio = body.size;
  }
  if (body.aspect_ratio !== undefined) {
    if (typeof body.aspect_ratio !== "string" || !IMAGE_RATIOS.includes(body.aspect_ratio))
      throw new Error("aspect_ratio must be a supported image aspect ratio");
    if (ratio && ratio !== body.aspect_ratio) throw new Error("image aspect ratio aliases must agree");
    ratio = body.aspect_ratio;
  }
  return ratio;
}

function images(body) {
  return Array.isArray(body && body.result && body.result.data)
    ? body.result.data.filter(function (item) {
        return record(item) && typeof item.url === "string" && item.url.startsWith("https://");
      })
    : [];
}

function imageCount(body) {
  return images(body).length;
}

export function buildSubmitRequest(ctx) {
  const body = { model: "gpt-image-2", prompt: ctx.requestBody.prompt, n: 1, resolution: imageResolution(ctx.requestBody.resolution) };
  const ratio = imageAspectRatio(ctx.requestBody);
  if (ratio !== undefined) body.size = ratio;
  if (ctx.requestBody.reference_images !== undefined) body.reference_images = ctx.requestBody.reference_images;
  return {
    url: ctx.baseUrl + "/v1/images/generations",
    method: "POST",
    headers: { Authorization: "Bearer " + ctx.apiKey, "Content-Type": "application/json", Accept: "application/json" },
    body,
    action: "generate",
  };
}

export function parseSubmitResponse(ctx, response) {
  const body = response.body;
  if (!record(body) || typeof body.id !== "string" || !body.id || body.object !== "generation.task") {
    throw new Error("ToAPIs did not return an image task");
  }
  // Persist the validated upstream tariff selector before any polling. The
  // query context has no requestBody, and result pixels are not tariff facts.
  const result = { taskId: body.id, taskData: body, state: { resolution: imageResolution(ctx.requestBody.resolution) } };
  const outputImages = images(body);
  if (body.status === "completed" && outputImages.length > 0) {
    result.immediate = { status: "SUCCESS", progress: "100%", remoteUrl: outputImages[0].url };
  } else if (body.status === "failed") {
    result.immediate = { status: "FAILURE", progress: "100%", reason: String((body.error && body.error.message) || "image generation failed") };
  }
  return result;
}

export function extractUsage(ctx) {
  return { image_count: Number((ctx.requestBody || {}).n) || 1, resolution: imageResolution((ctx.requestBody || {}).resolution) };
}

export function extractUsageOnComplete(ctx, result, body) {
  const facts = { image_count: imageCount(body) };
  // Older tasks did not persist resolution. Keep their historical usage
  // unchanged instead of inventing a verified tariff fact for them.
  if (record(ctx.state) && ctx.state.resolution !== undefined) {
    facts.resolution = imageResolution(ctx.state.resolution);
  }
  return facts;
}

export function buildQueryRequest(ctx) {
  return {
    url: ctx.baseUrl + "/v1/images/generations/" + encodeURIComponent(ctx.taskId),
    method: "GET",
    headers: { Authorization: "Bearer " + ctx.apiKey, Accept: "application/json" },
  };
}

export function parseTaskResult(ctx, body) {
  if (!record(body) || body.object !== "generation.task" || body.id !== ctx.taskId) {
    return { status: "UNKNOWN", reason: "unrecognized image task" };
  }
  const progress = Number(body.progress);
  const pct = Number.isFinite(progress) ? Math.max(0, Math.min(100, progress)) + "%" : "0%";
  if (body.status === "completed") {
    const outputImages = images(body);
    if (outputImages.length === 0) return { status: "UNKNOWN", reason: "completed task has no image" };
    return { status: "SUCCESS", progress: "100%", remoteUrl: outputImages[0].url };
  }
  if (body.status === "failed") {
    return { status: "FAILURE", progress: "100%", reason: String((body.error && body.error.message) || "image generation failed") };
  }
  if (body.status === "queued" || body.status === "pending") return { status: "SUBMITTED", progress: pct };
  if (body.status === "in_progress") return { status: "IN_PROGRESS", progress: pct };
  return { status: "UNKNOWN", reason: "unrecognized image status" };
}

function response(task) {
  const data = record(task.data) ? task.data : {};
  const status = task.status === "SUCCESS" ? "completed" : task.status === "FAILURE" ? "failed" : task.status === "IN_PROGRESS" ? "in_progress" : "queued";
  const progress = Number(String(task.progress || "").replace("%", ""));
  return {
    id: task.task_id,
    object: "generation.task",
    model: "gpt-image-2",
    status,
    progress: status === "completed" ? 100 : Number.isFinite(progress) ? progress : 0,
    created_at: data.created_at || task.created_at || 0,
    ...(status === "completed" && record(data.result) ? { result: Object.assign({}, data.result, { data: images(data) }) } : {}),
    ...(status === "failed" ? { error: data.error || { message: task.fail_reason || "image generation failed" } } : {}),
  };
}

export const native = {
  decodeSubmit(ctx) {
    if (!ctx.body || ctx.body.kind !== "json" || !record(ctx.body.value)) throw new Error("JSON body required");
    const body = ctx.body.value;
    if (body.model !== "yunshu-internal-gpt-image-2" || typeof body.prompt !== "string" || !body.prompt.trim()) {
      throw new Error("gpt-image-2 and a prompt are required");
    }
    if (body.n !== undefined && body.n !== 1) throw new Error("only one image per task is supported");
    const ratio = imageAspectRatio(body);
    if (
      body.reference_images !== undefined &&
      (!Array.isArray(body.reference_images) ||
        body.reference_images.length > 6 ||
        body.reference_images.some(function (url) {
          return typeof url !== "string" || !url.startsWith("https://");
        }))
    ) {
      throw new Error("reference_images must contain up to six HTTPS URLs");
    }
    const requestBody = { model: "yunshu-internal-gpt-image-2", prompt: body.prompt, n: 1, resolution: imageResolution(body.resolution) };
    if (ratio !== undefined) requestBody.size = ratio;
    if (body.reference_images !== undefined) requestBody.reference_images = body.reference_images;
    return { kind: "submit", model: "yunshu-internal-gpt-image-2", action: "generate", requestBody };
  },
  renderSubmit(ctx, task) {
    return response(task);
  },
  renderTask(ctx, task) {
    return response(task);
  },
  error(ctx, error) {
    return { error: { code: error.code, message: error.message } };
  },
};
