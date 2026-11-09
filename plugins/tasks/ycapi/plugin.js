// The host prepends the approved server-only catalog from pkg/ycapi. Neither
// procurement lanes nor upstream responses are copied to public task results.
const MODELS = globalThis.ycapiApprovedCatalog;
const schema = (model) => {
  if (model.kind === "audio")
    return {
      input_tokens: { type: "number", unit: "token", description: { en: "Text input unit price", zh: "文本输入单价" } },
      output_tokens: { type: "number", unit: "token", description: { en: "Audio output unit price", zh: "音频输出单价" } },
    };
  const quantity = model.kind === "video" ? "seconds" : "image_count";
  return {
    [quantity]: {
      type: "number",
      unit: model.kind === "video" ? "second" : "count",
      description: {
        en: model.kind === "video" ? "Video generation unit price" : "Image generation unit price",
        zh: model.kind === "video" ? "视频生成单价" : "图片生成单价",
      },
    },
    spec: { enum: model.offers.map((offer) => offer.id), description: { en: "Generation specification", zh: "生成规格" } },
  };
};

export const meta = {
  apiVersion: 1,
  key: "ycapi",
  name: "YCAPI",
  version: "1.0.3",
  icon: "text:YC",
  author: { name: "Yunshu" },
  baseUrl: "https://ycapi.token6688.com",
  fetchMode: "per_task",
  description: { en: "Approved YCAPI image, video and speech generation", zh: "已批准的YCAPI图片、视频与语音生成" },
  models: MODELS.map((model) => model.id),
  usageSchema: {},
  usageProfiles: MODELS.map((model) => ({
    models: [model.id],
    schema: schema(model),
    examples:
      model.kind === "audio"
        ? [{ label: "Speech", facts: { input_tokens: 1000, output_tokens: 8192 } }]
        : model.offers.map((offer) => ({
            label: offer.id,
            facts: model.kind === "video" ? { seconds: Number(offer.params.duration), spec: offer.id } : { image_count: 1, spec: offer.id },
          })),
  })),
  protocols: [
    { name: "openai_image", models: MODELS.filter((m) => m.kind === "image").map((m) => m.id), retainResult: true },
    { name: "openai_video", models: MODELS.filter((m) => m.kind === "video").map((m) => m.id) },
  ],
};

function modelFor(ctx) {
  const model = MODELS.find((m) => m.id === (ctx.upstreamModel || ctx.model));
  if (!model) throw new Error("Model is not included in the approved catalog");
  return model;
}

function object(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : null;
}
function text(value, limit) {
  if (typeof value !== "string" || !value.trim() || Array.from(value).length > limit) throw new Error("Text is empty or exceeds the model limit");
  return value;
}
function equal(a, b) {
  return String(a).toLowerCase() === String(b).toLowerCase();
}

function normalize(source, model) {
  if (!object(source)) throw new Error("A JSON object is required");
  const allowed =
    model.kind === "audio"
      ? ["model", "input", "voice", "response_format", "speed", "quote_id"]
      : ["model", "prompt", "n", "count", "size", "resolution", "quality", "seconds", "duration", "ratio", "aspect_ratio", "metadata", "response_format", ...(model.kind === "image" ? ["reference_images"] : [])];
  for (const key of Object.keys(source)) if (!allowed.includes(key)) throw new Error("Unsupported generation parameter: " + key);
  if (source.model !== undefined && source.model !== model.id) throw new Error("Generation model does not match the route");
  if (model.kind === "audio") {
    const params = { input: text(source.input, 2000) };
    for (const key of ["voice", "response_format", "speed"]) {
      const field = model.fields[key];
      if (!field) {
        if (source[key] !== undefined) throw new Error("Unsupported speech option");
        continue;
      }
      const value = source[key] === undefined ? field.default : source[key];
      if (field.enum && field.enum.length && !field.enum.some((v) => equal(v, value))) throw new Error("Unsupported speech option");
      if (key === "speed" && (typeof value !== "number" || !Number.isFinite(value) || value < field.min || value > field.max))
        throw new Error("Unsupported speech speed");
      if (value !== undefined && value !== null) params[key] = value;
    }
    let estimatedInput = 0;
    for (const character of params.input) {
      const point = character.codePointAt(0);
      if (point <= 127) estimatedInput += 1;
      else if (point <= 2047) estimatedInput += 2;
      else if (point <= 65535) estimatedInput += 3;
      else estimatedInput += 4;
    }
    return { params, offer: model.offers[0], facts: { input_tokens: estimatedInput, output_tokens: 8192 } };
  }
  const imagePrefix = model.upstream === "kling-image-o3" && Array.isArray(source.reference_images) && source.reference_images.length
    ? "参考图片按上传顺序编号为图1。基于图1执行以下编辑，保留未要求修改的内容：\n" : "";
  const prompt = text(source.prompt, model.upstream === "kling-image-o3" ? 2500 - Array.from(imagePrefix).length : 16000);
  for (const name of ["n", "count"])
    if (source[name] !== undefined && Number(source[name]) !== 1) throw new Error("Only one output is available for this specification");
  const metadata = source.metadata === undefined ? {} : object(source.metadata);
  if (!metadata) throw new Error("Invalid generation metadata");
  for (const key of Object.keys(metadata))
    if (!["quality", "ratio", "aspect_ratio", "resolution", "size"].includes(key)) throw new Error("Unsupported generation metadata");
  const values = {};
  for (const name of ["size", "resolution", "quality", "seconds", "duration", "ratio", "aspect_ratio"]) {
    const candidates = [source[name], metadata[name]].filter((v) => v !== undefined);
    if (candidates.some((v) => !["string", "number"].includes(typeof v)) || candidates.some((v) => !equal(v, candidates[0])))
      throw new Error("Conflicting generation parameters");
    if (candidates.length) values[name] = candidates[0];
  }
  if (values.seconds !== undefined && values.duration !== undefined && !equal(values.seconds, values.duration)) throw new Error("Conflicting video duration");
  if (values.ratio !== undefined && values.aspect_ratio !== undefined && !equal(values.ratio, values.aspect_ratio)) throw new Error("Conflicting aspect ratio");
  let imageControl, imageRatio, imageSizeIsRatio = false;
  if (model.kind === "image") {
    imageControl = object(model.image_control);
    if (!imageControl || !["size", "aspect_ratio"].includes(imageControl.ratio_field) || !Array.isArray(imageControl.ratios) || !imageControl.ratios.length)
      throw new Error("Image aspect ratio controls are unavailable");
    imageSizeIsRatio = values.size !== undefined && (String(values.size).includes(":") || imageControl.ratios.some((ratio) => equal(ratio, values.size)));
    const ratios = [values.ratio, values.aspect_ratio, imageSizeIsRatio ? values.size : undefined].filter((value) => value !== undefined);
    if (ratios.some((value) => !equal(value, ratios[0]))) throw new Error("Conflicting aspect ratio");
    if (ratios.length) {
      imageRatio = imageControl.ratios.find((ratio) => equal(ratio, ratios[0]));
      if (imageRatio === undefined) throw new Error("Unsupported image aspect ratio");
    }
  }
  const candidates = model.offers.filter((offer) => {
    const p = offer.params;
    if (model.kind === "image") {
      const ratio = imageRatio === undefined ? p[imageControl.ratio_field] : imageRatio;
      if (!Array.isArray(offer.image_ratios) || !offer.image_ratios.some((allowed) => equal(allowed, ratio))) return false;
    }
    for (const [key, value] of Object.entries(values)) {
      // Image shape is independent of the existing resolution/quality price
      // tier. Keep that tier's original identity and procurement route.
      if (model.kind === "image" && (key === "ratio" || key === "aspect_ratio" || key === "size" && imageSizeIsRatio)) continue;
      let expected;
      if (key === "seconds" || key === "duration") expected = p.duration;
      else if (key === "ratio" || key === "aspect_ratio") expected = p.aspect_ratio || (String(p.size).includes(":") ? p.size : "1:1");
      else if (key === "resolution") expected = p.resolution || (!String(p.size).includes(":") ? p.size : undefined);
      else if (key === "size") {
        if (model.kind === "image") expected = p.resolution || (!String(p.size).includes(":") ? p.size : undefined);
        else expected = String(value).includes(":") ? p.aspect_ratio : p.resolution;
      } else expected = p[key];
      if (expected === undefined || !equal(value, expected)) return false;
    }
    return true;
  });
  if (candidates.length !== 1) throw new Error("Select a complete approved generation specification");
  const offer = candidates[0];
  const params = { ...offer.params, prompt };
  if (model.kind === "image") {
    const ratio = imageRatio === undefined ? params[imageControl.ratio_field] : imageRatio;
    if (!imageControl.ratios.some((allowed) => equal(allowed, ratio))) throw new Error("Unsupported image aspect ratio");
    params[imageControl.ratio_field] = ratio;
    const edit = object(model.image_edit);
    const refs = imageReferences(source, edit ? edit.max_images : 0, edit ? edit.max_bytes : 5242880);
    if (refs.length) {
      if (!Array.isArray(edit.offers) || !edit.offers.includes(offer.id)) throw new Error("Image editing has not been verified for this specification");
      if (model.upstream === "gemini-nano-banana-2.1-image" && refs.some((ref) => typeof ref !== "string" || !ref.startsWith("https://assets.token6688.com/sha256/")))
        throw new Error("Upload the reference image before editing with this model");
      params.mode = "image-edit";
      params.images = refs;
      if (imagePrefix) params.prompt = imagePrefix + prompt;
    }
  }
  return {
    offer,
    params,
    facts: model.kind === "video" ? { seconds: Number(offer.params.duration), spec: offer.id } : { image_count: 1, spec: offer.id },
  };
}

function decode(ctx, kind) {
  if (!ctx.body || ctx.body.kind !== "json") throw new Error("A JSON request is required");
  const model = modelFor(ctx);
  if (model.kind !== kind) throw new Error("This model does not support the requested operation");
  normalize(ctx.body.value, model);
  return { kind: "submit", model: ctx.model, action: kind, requestBody: ctx.body.value };
}

export function buildSubmitRequest(ctx) {
  const model = modelFor(ctx);
  const normalized = normalize(ctx.requestBody, model);
  if (typeof ctx.publicTaskId !== "string" || !ctx.publicTaskId) throw new Error("A durable task identity is required");
  const fields = { lane_id: normalized.offer.lane, client_request_id: ctx.publicTaskId };
  const body =
    model.kind === "video"
      ? { ...normalized.params, model: model.upstream, ...fields }
      : { model_id: model.upstream, modality: model.kind, operation: model.kind === "audio" ? "speech" : normalized.params.images ? "edit" : "generate", params: normalized.params,
          ...(normalized.params.images ? { attachments: normalized.params.images } : {}), ...fields };
  return {
    url: ctx.baseUrl + (model.kind === "video" ? "/v1/videos/generations" : "/api/v1/model-runtime/invoke"),
    method: "POST",
    headers: { Authorization: "Bearer " + ctx.apiKey, "Content-Type": "application/json" },
    body,
  };
}

function publicURL(value) {
  if (typeof value !== "string" || value.length > 16384 || !/^https:\/\/[^\s\\/@]+(?:[/:?#]|$)/.test(value)) return null;
  return value;
}

function outputs(body, kind) {
  const result = object(body.result) || {};
  for (const list of [body.outputs, result.outputs, result[kind + "s"], body[kind + "s"], body.output_urls, body.result_urls]) {
    if (!Array.isArray(list)) continue;
    const urls = list.map((v) => publicURL(typeof v === "string" ? v : (object(v) || {}).url || v.output_url || v.result_url)).filter(Boolean);
    if (urls.length) return urls.slice(0, 1);
  }
  for (const value of [body.output_url, body.result_url, result.output_url, result.result_url, body.audio_url, result.audio_url]) {
    const url = publicURL(value);
    if (url) return [url];
  }
  return [];
}

function snapshot(body, model, expectedId) {
  if (!object(body)) throw new Error("Invalid upstream task response");
  const id = body.task_id || body.id;
  if (typeof id !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$/.test(id) || (expectedId && id !== expectedId))
    throw new Error("Upstream task identity mismatch");
  const modelMetadata = object(body.model);
  const responseModel = modelMetadata ? modelMetadata.public_model : body.model || body.model_id;
  if (modelMetadata && (typeof responseModel !== "string" || !responseModel)) throw new Error("Invalid upstream task model");
  if (responseModel && responseModel !== model.upstream && responseModel !== model.id) throw new Error("Upstream task model mismatch");
  const status = typeof body.status === "string" ? body.status.toLowerCase() : "unknown";
  const data = { id, kind: model.kind, status, is_final: body.is_final === true, outputs: outputs(body, model.kind) };
  const error = object(body.error) || {};
  const code = typeof error.code === "string" ? error.code.toLowerCase() : "";
  if (["insufficient_balance", "insufficient_quota", "quota_exceeded"].includes(code)) data.failureReason = "Upstream account balance is insufficient";
  else if (["content_policy_violation", "content_filter", "moderation_blocked"].includes(code))
    data.failureReason = "Generation was rejected by content moderation";
  else if (["task_timeout", "generation_timeout"].includes(code)) data.failureReason = "Upstream generation timed out";
  else if (["pinned_channel_unavailable", "model_unknown"].includes(code)) data.failureReason = "The configured upstream model is unavailable";
  const usage = object(body.usage);
  if (usage) {
    const input = usage.input_tokens ?? usage.prompt_tokens;
    const output = usage.output_tokens ?? usage.completion_tokens;
    if ([input, output].every((v) => Number.isInteger(v) && v >= 0 && v <= 2147483647)) data.usage = { input_tokens: input, output_tokens: output };
  }
  return data;
}

function taskResult(data) {
  if (data.kind === "audio" && data.is_final && data.status === "completed" && !data.usage)
    return { status: "UNKNOWN", reason: "Upstream audio usage is unavailable" };
  if (data.is_final && data.status === "completed" && data.outputs.length) return { status: "SUCCESS", progress: "100%" };
  if (data.is_final && ["failed", "cancelled", "canceled"].includes(data.status))
    return { status: "FAILURE", reason: data.failureReason || "Upstream generation failed", progress: "100%" };
  if (["pending", "queued", "processing", "running", "completed", "failed", "cancelled", "canceled"].includes(data.status))
    return { status: "IN_PROGRESS", progress: "0%" };
  return { status: "UNKNOWN" };
}

export function parseSubmitResponse(ctx, response) {
  const model = modelFor(ctx);
  const data = snapshot(response.body, model);
  const normalized = normalize(ctx.requestBody, model);
  if (model.kind === "audio") data.format = normalized.params.response_format;
  const result = taskResult(data);
  const state = { facts: normalized.facts, kind: model.kind, snapshot: data };
  if (model.kind === "audio") state.format = normalized.params.response_format;
  // Images must preserve a durable artifact. Audio must run the completion
  // usage hook, including when invoke already returned a ready asset; the
  // conservative output estimate is never a final usage statement.
  return { taskId: data.id, taskData: data, state, ...(model.kind === "video" && ["SUCCESS", "FAILURE"].includes(result.status) ? { immediate: result } : {}) };
}

export function extractUsage(ctx) {
  return normalize(ctx.requestBody, modelFor(ctx)).facts;
}
export function buildQueryRequest(ctx) {
  const model = modelFor(ctx);
  return {
    url: ctx.baseUrl + (model.kind === "video" ? "/v1/tasks/" : "/api/v1/model-runtime/tasks/") + encodeURIComponent(ctx.taskId),
    method: "GET",
    headers: { Authorization: "Bearer " + ctx.apiKey },
  };
}
export function parseTaskResult(ctx, body) {
  const data = snapshot(body, modelFor(ctx), ctx.taskId);
  if (data.kind === "audio" && ctx.state) data.format = ctx.state.format;
  // The host owns task.data and stores the raw poll response. Normalized safe
  // output coordinates belong in durable plugin state, not an ignored field.
  return { ...taskResult(data), state: { ...(object(ctx.state) || {}), snapshot: data } };
}
export function extractUsageOnComplete(ctx, _result, body) {
  if (modelFor(ctx).kind === "audio") {
    const usage = object(body.usage);
    if (!usage) return {};
    const input = usage.input_tokens ?? usage.prompt_tokens;
    const output = usage.output_tokens ?? usage.completion_tokens;
    if ([input, output].every((v) => Number.isInteger(v) && v >= 0 && v <= 2147483647)) return { input_tokens: input, output_tokens: output };
  }
  return {};
}
export function listArtifacts(task) {
  const data = task.state && task.state.snapshot;
  if (task.status !== "SUCCESS" || !data || !data.outputs || !publicURL(data.outputs[0])) return [];
  const kind = data.kind;
  let mimeType = "image/png";
  if (kind === "video") mimeType = "video/mp4";
  if (kind === "audio") mimeType = data.format === "wav" ? "audio/wav" : "audio/mpeg";
  return [{ key: kind, type: kind, mimeType }];
}
export function buildContentRequest(ctx) {
  const data = ctx.state && ctx.state.snapshot;
  const url = data && data.outputs && publicURL(data.outputs[0]);
  if (!url || ctx.artifactKey !== data.kind) throw new Error("artifact_not_found");
  return { url, method: ctx.clientRequest.method, credentialless: true };
}
export const protocols = {
  openai_image: {
    decodeRequest: function (ctx) {
      const model = modelFor(ctx);
      if (model.kind !== "image") throw new Error("This model does not support image generation");
      const edit = object(model.image_edit);
      const body = decodeImageRequest(ctx, edit ? edit.max_images : 0, edit ? edit.max_bytes : 5242880);
      normalize(body, model);
      return { kind: "submit", model: ctx.model, action: body.reference_images ? "image_edit" : "image", requestBody: body };
    },
    render: function (ctx, _task) {
      if (!ctx.artifacts || !ctx.artifacts.image) throw new Error("Image artifact is unavailable");
      return { data: [{ url: ctx.artifacts.image.url }] };
    },
  },
  openai_video: {
    decodeRequest: function (ctx) {
      return decode(ctx, "video");
    },
    render: function (_ctx, task) {
      return { url: task.data && outputs(task.data, "video")[0] };
    },
  },
};
