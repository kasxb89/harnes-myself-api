// The host prepends the approved server-only catalog from pkg/ycapi. Neither
// procurement lanes nor upstream responses are copied to public task results.
const MODELS = globalThis.ycapiApprovedCatalog;
const MEDIA_CONTRACTS = globalThis.ycapiMediaContracts || [];
const NEW_MEDIA = MEDIA_CONTRACTS.filter((model) => !MODELS.some((legacy) => legacy.id === model.id));
const ALL_MODELS = [...MODELS, ...NEW_MEDIA];
const PRICE_CONTROLS = ["resolution", "quality", "mode", "aspect_ratio"];
const PRICE_CONTROL_LABELS = { resolution: "分辨率", quality: "质量", mode: "模式", aspect_ratio: "画幅比例" };
function usageMenu(model, name) {
  const contract = MEDIA_CONTRACTS.find((entry) => entry.id === model.id);
  const values = [""];
  const add = (value) => { if (typeof value === "string" && !values.includes(value)) values.push(value); };
  const field = contract && contract.param_schema[name];
  if (field) {
    for (const value of field.enum || []) add(value);
    for (const option of field.options || []) add(option.value);
    for (const menu of Object.values(field.enum_values_by_quality || {}))
      if (Array.isArray(menu)) for (const value of menu) add(value);
  }
  if (contract && ["resolution", "aspect_ratio"].includes(name)) {
    for (const value of (contract.param_schema.size || {}).enum || [])
      if (typeof value === "string" && value.includes(":") === (name === "aspect_ratio")) add(value);
  }
  for (const offer of model.offers || []) {
    add(offer.params[name]);
    if (["resolution", "aspect_ratio"].includes(name) && typeof offer.params.size === "string" &&
        offer.params.size.includes(":") === (name === "aspect_ratio")) add(offer.params.size);
  }
  return values;
}
function mediaBooleanFact(params, name) {
  // An absent flag is false as a billing fact; this does not assert the
  // supplier's default output is silent or that its default mode is draft.
  if (params[name] === undefined) return false;
  if (typeof params[name] !== "boolean") throw new Error("Invalid native media boolean: " + name);
  return params[name];
}
function legacyMediaFacts(model, params, facts) {
  const images = Array.isArray(params.images) ? params.images.length : 0;
  return { ...facts, ...(model.kind === "video" ? { output_count: 1, total_output_seconds: Number(facts.seconds) } : {}), input_video_seconds: 0, input_audio_seconds: 0, input_image_count: images,
    generate_audio: mediaBooleanFact(params, "generate_audio"), draft: mediaBooleanFact(params, "draft"),
    intent: images ? "reference" : "text", resolution: params.resolution || (typeof params.size === "string" && !params.size.includes(":") ? params.size : ""),
    quality: params.quality || "", mode: params.mode || "", aspect_ratio: params.aspect_ratio || (typeof params.size === "string" && params.size.includes(":") ? params.size : "") };
}
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
    ...(model.kind === "video" ? {
      output_count: { type: "number", unit: "count", unitLabel: { en: "video", zh: "条" }, description: { en: "Video generation unit price", zh: "视频生成单价" } },
      total_output_seconds: { type: "number", unit: "second", description: { en: "Batch video generation unit price", zh: "批量视频生成单价" } },
    } : {}),
    input_video_seconds: { type: "number", unit: "second", description: { en: "Reference video unit price", zh: "参考视频单价" } },
    input_audio_seconds: { type: "number", unit: "second", description: { en: "Reference audio unit price", zh: "参考音频单价" } },
    input_image_count: { type: "number", unit: "count", description: { en: "Reference image unit price", zh: "参考图片单价" } },
    generate_audio: { type: "boolean", description: { en: "Generate output audio", zh: "生成视频原声" } },
    draft: { type: "boolean", description: { en: "Draft generation", zh: "草稿生成" } },
    intent: { enum: ["text", "reference", "frames", "transform"], description: { en: "Generation intent", zh: "生成意图" } },
    ...Object.fromEntries(PRICE_CONTROLS.map((name) => [name, { enum: usageMenu(model, name), description: { en: name, zh: PRICE_CONTROL_LABELS[name] } }])),
    spec: { enum: [...(model.offers || []).map((offer) => offer.id), "media-v2"], description: { en: "Generation specification", zh: "生成规格" } },
  };
};

export const meta = {
  apiVersion: 1,
  key: "ycapi",
  name: "YCAPI",
  version: "1.0.4",
  icon: "text:YC",
  author: { name: "Yunshu" },
  baseUrl: "https://ycapi.token6688.com",
  fetchMode: "per_task",
  description: { en: "Approved YCAPI image, video and speech generation", zh: "已批准的YCAPI图片、视频与语音生成" },
  models: ALL_MODELS.map((model) => model.id),
  usageSchema: {},
  usageProfiles: [...MODELS.map((model) => ({
    models: [model.id],
    schema: schema(model),
    examples:
      model.kind === "audio"
        ? [{ label: "Speech", facts: { input_tokens: 1000, output_tokens: 8192 } }]
        : model.offers.map((offer) => ({
            label: offer.id,
            facts: legacyMediaFacts(model, offer.params, model.kind === "video" ? { seconds: Number(offer.params.duration), spec: offer.id } : { image_count: 1, spec: offer.id }),
          })),
  })), ...NEW_MEDIA.map((model) => ({ models: [model.id], schema: schema(model) }))],
  protocols: [
    { name: "openai_image", models: ALL_MODELS.filter((m) => m.kind === "image").map((m) => m.id), retainResult: true },
    { name: "openai_video", models: ALL_MODELS.filter((m) => m.kind === "video").map((m) => m.id) },
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

// This context member is installed by the Host after quote, asset, intent and
// exact native-envelope validation. No requestBody field can create this path.
function mediaV2(ctx, requireTaskIdentity = false) {
  if (ctx.mediaGenerationV2Error !== undefined) throw new Error("Invalid Host media generation context");
  if (ctx.mediaGenerationV2 === undefined) return null;
  const frozen = object(ctx.mediaGenerationV2);
  const request = frozen && object(frozen.nativeRequest);
  const contract = MEDIA_CONTRACTS.find((model) => model.id === ctx.model);
  if (!frozen || !request || !contract || frozen.kind !== contract.kind || request.modality !== contract.kind ||
      request.model_id !== contract.native_id || !object(request.params) || typeof request.lane_id !== "string" || !request.lane_id ||
      typeof request.client_request_id !== "string" || !request.client_request_id || !object(frozen.usageFacts) ||
      typeof frozen.retailCny !== "string" || !/^\d+(?:\.\d+)?$/.test(frozen.retailCny))
    throw new Error("Invalid Host media generation context");
  if (Object.keys(request).some((key) => !["model_id", "modality", "operation", "params", "lane_id", "client_request_id"].includes(key)))
    throw new Error("Invalid Host native request envelope");
  if (contract.kind === "image" ? !["generate", "edit"].includes(request.operation) : request.operation !== undefined)
    throw new Error("Invalid Host media operation");
  if (requireTaskIdentity && (typeof ctx.publicTaskId !== "string" || !ctx.publicTaskId || request.client_request_id !== ctx.publicTaskId))
    throw new Error("Host media task identity mismatch");
  const quantity = contract.kind === "video" ? "seconds" : "image_count";
  const profile = schema(MODELS.find((model) => model.id === contract.id) || contract);
  // Historical quoted facts predate aggregate meters; zero preserves their
  // original expression instead of retroactively multiplying legacy seconds.
  const facts = { ...frozen.usageFacts };
  if (contract.kind === "video") for (const key of ["output_count", "total_output_seconds"]) if (facts[key] === undefined) facts[key] = 0;
  if (Object.keys(facts).length !== Object.keys(profile).length ||
      Object.keys(facts).some((key) => !Object.prototype.hasOwnProperty.call(profile, key)))
    throw new Error("Invalid Host media usage facts");
  for (const key of Object.keys(profile)) {
    const value = facts[key], field = profile[key];
    if (field.enum ? !field.enum.includes(value) : field.type === "boolean" ? typeof value !== "boolean" : typeof value !== "number" || !Number.isFinite(value) || value < 0 ||
        value > (field.unit === "count" ? 128 : 3600) || field.unit === "count" && !Number.isInteger(value))
      throw new Error("Invalid Host media usage facts");
  }
  const batch = frozen.batch === undefined ? null : mediaBatch(frozen.batch);
  const count = request.params.count === undefined ? 1 : request.params.count;
  if (contract.kind === "video" && count !== 1 && !batch) throw new Error("Native video batch is not qualified");
  if (batch && (contract.kind !== "video" || count !== batch.requested_output_count || facts.output_count !== count ||
      facts.total_output_seconds !== batch.total_output_seconds)) throw new Error("Invalid Host media batch facts");
  if (facts[quantity] <= 0 && !(batch && batch.meter === "output_count")) throw new Error("Invalid Host media usage facts");
  return { contract, request, facts, batch };
}

function mediaBatch(value) {
  const b = object(value);
  if (!b || b.schema_version !== 1 || !Number.isInteger(b.requested_output_count) || b.requested_output_count < 1 || b.requested_output_count > 50 ||
      b.expected_main_output_count !== b.requested_output_count || !["output_count", "total_output_seconds"].includes(b.meter) ||
      typeof b.output_duration_seconds !== "number" || !Number.isFinite(b.output_duration_seconds) || b.output_duration_seconds < 0 ||
      b.total_output_seconds !== b.output_duration_seconds * b.requested_output_count || b.total_output_seconds > 3600 ||
      b.meter === "total_output_seconds" && b.total_output_seconds <= 0 || b.admission_unit !== "native_operation" || b.admission_weight !== 1 ||
      b.partial_policy !== "retain_reservation_until_complete" || typeof b.policy_revision !== "string" || !/^[a-f0-9]{64}$/.test(b.policy_revision))
    throw new Error("Invalid Host native batch contract");
  return { ...b };
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
    facts: legacyMediaFacts(model, params, model.kind === "video" ? { seconds: Number(offer.params.duration), spec: offer.id } : { image_count: 1, spec: offer.id }),
  };
}

function decode(ctx, kind) {
  if (!ctx.body || ctx.body.kind !== "json") throw new Error("A JSON request is required");
  const v2 = mediaV2(ctx);
  if (v2) {
    if (v2.contract.kind !== kind) throw new Error("This model does not support the requested operation");
    return { kind: "submit", model: ctx.model, action: kind, requestBody: ctx.body.value };
  }
  const model = modelFor(ctx);
  if (model.kind !== kind) throw new Error("This model does not support the requested operation");
  normalize(ctx.body.value, model);
  return { kind: "submit", model: ctx.model, action: kind, requestBody: ctx.body.value };
}

export function buildSubmitRequest(ctx) {
  const v2 = mediaV2(ctx, true);
  if (v2) return {
    url: ctx.baseUrl + "/api/v1/model-runtime/invoke", method: "POST",
    headers: { Authorization: "Bearer " + ctx.apiKey, "Content-Type": "application/json" },
    body: JSON.parse(JSON.stringify(v2.request)),
  };
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
    if (list.length > 64) throw new Error("Too many upstream media outputs");
    const urls = list.map((v) => publicURL(typeof v === "string" ? v : (object(v) || {}).url || v.output_url || v.result_url));
    if (urls.some((url) => !url)) throw new Error("Invalid upstream media output");
    if (urls.length) return urls;
  }
  for (const value of [body.output_url, body.result_url, result.output_url, result.result_url, body.audio_url, result.audio_url]) {
    const url = publicURL(value);
    if (url) return [url];
  }
  return [];
}

// Pre-policy legacy tasks retained one valid URL. Their saved receipts must
// keep that parser and artifact identity even after the active plugin changes.
// New submissions and native V2 continue through their strict full-output paths.
function legacyFirstOutput(body, kind) {
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

function mediaOutputList(body, kind) {
  const result = object(body.result) || {};
  // data[] is the documented complete ordered carrier. Scalar aliases point
  // only at its first result and must never replace or duplicate that list.
  for (const list of [body.data, body.outputs, result.data, result.outputs, body.output_urls, body.result_urls]) {
    if (Array.isArray(list) && list.length) return list;
  }
  for (const container of [result, body]) {
    const grouped = [];
    for (const key of Object.keys(container)) {
      if (!["images", "videos", "audios", "files"].includes(key) || !Array.isArray(container[key])) continue;
      for (const entry of container[key]) grouped.push(typeof entry === "string" ? { url: entry, type: key.slice(0, -1) }
        : { type: key.slice(0, -1), ...(object(entry) || {}) });
    }
    if (grouped.length) return grouped;
  }
  const url = body.output_url || body.result_url || body.url || result.output_url || result.result_url || body[kind + "_url"];
  return url ? [{ url, type: body.result_type || body.type || kind }] : [];
}

function mediaArtifacts(body, kind, outputFormat) {
  const entries = mediaOutputList(body, kind);
  if (entries.length > 64) throw new Error("Too many upstream media outputs");
  const defaults = { image: "image/png", video: "video/mp4", audio: "audio/mpeg", file: "application/octet-stream" };
  const formats = { png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", webp: "image/webp", gif: "image/gif", svg: "image/svg+xml" };
  return entries.map((entry, index) => {
    const value = typeof entry === "string" ? { url: entry } : object(entry);
    const url = value && publicURL(value.url || value.output_url || value.result_url);
    if (!url) throw new Error("Invalid upstream media output");
    let mime = value.mime_type || value.mimeType || value.content_type || "";
    if (mime && (typeof mime !== "string" || !/^[A-Za-z0-9!#$&^_.+-]+\/[A-Za-z0-9!#$&^_.+-]+$/.test(mime)))
      throw new Error("Invalid upstream media MIME type");
    mime = mime.toLowerCase();
    const mimeKind = mime.split("/")[0];
    let type = value.type || value.kind || value.modality || (mime ? ["image", "video", "audio"].includes(mimeKind) ? mimeKind : "file" : kind);
    if (mime === "image/svg+xml" || !mime && outputFormat === "svg") type = "file";
    if (!["image", "video", "audio", "file"].includes(type)) throw new Error("Unsupported upstream media output type");
    if (!mime) mime = type === "image" || outputFormat === "svg" ? formats[outputFormat] || defaults[type] : defaults[type];
    if (type !== "file" && !mime.startsWith(type + "/")) throw new Error("Upstream media type mismatch");
    return { key: entries.length === 1 ? type : type + "-" + (index + 1), type, mimeType: mime, url };
  });
}

function mediaSnapshot(body, contract, expectedId, outputFormat, batch) {
  if (!object(body)) throw new Error("Invalid upstream media task response");
  const result = object(body.result) || {};
  const id = body.task_id || body.id || result.task_id;
  if (typeof id !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$/.test(id) || expectedId && id !== expectedId)
    throw new Error("Upstream task identity mismatch");
  for (const alias of [body.task_id, body.id, result.task_id]) if (alias !== undefined && alias !== id)
    throw new Error("Upstream task identity mismatch");
  const model = object(body.model);
  const responseModel = model ? model.public_model : body.model || body.model_id;
  if (responseModel && ![contract.id, contract.native_id, contract.upstream_public_id].includes(responseModel))
    throw new Error("Upstream task model mismatch");
  const artifacts = mediaArtifacts(body, contract.kind, outputFormat);
  const status = typeof body.status === "string" ? body.status.toLowerCase() : "unknown";
  const data = { id, kind: contract.kind, status, is_final: body.is_final === true, artifacts, outputs: artifacts.map((entry) => entry.url) };
  if (batch) {
    const entries = mediaOutputList(body, contract.kind);
    const identities = new Map(), urls = new Map(), providerIndices = new Map(), groupIndices = new Map();
    let received = 0;
    for (let i = 0; i < artifacts.length; i++) {
      const artifact = artifacts[i], entry = object(entries[i]) || {};
      if (entry.role !== undefined && entry.output_role !== undefined && entry.role !== entry.output_role)
        throw new Error("Conflicting upstream batch artifact roles");
      const explicit = entry.role === undefined ? entry.output_role : entry.role;
      const declaredType = entry.type || entry.kind || entry.modality;
      const declaredMIME = entry.mime_type || entry.mimeType || entry.content_type;
      if (!declaredType && !declaredMIME) { artifact.type = "file"; artifact.mimeType = "application/octet-stream"; }
      const typedVideo = artifact.type === "video" && (declaredType === "video" || typeof declaredMIME === "string" && declaredMIME.toLowerCase().startsWith("video/"));
      const role = explicit === undefined ? typedVideo ? "main" : artifact.type === "image" ? "preview" : "auxiliary" : explicit;
      if (!["main", "preview", "auxiliary", "layer", "aux"].includes(role)) throw new Error("Invalid upstream batch artifact role");
      artifact.role = ["layer", "aux"].includes(role) ? "auxiliary" : role;
      if (artifact.role !== "main" || !typedVideo) { if (artifact.role === "main") artifact.role = "auxiliary"; continue; }
      // A signed alias of the same URL, or multiple encodings of one explicit
      // logical result, must never make a partial batch look complete.
      const urlKey = artifact.url.split(/[?#]/)[0];
      if (entry.output_id !== undefined && (typeof entry.output_id !== "string" || !entry.output_id || entry.output_id !== entry.output_id.trim() || entry.output_id.length > 256 ||
          Array.from(entry.output_id).some((ch) => ch.charCodeAt(0) < 32 || ch.charCodeAt(0) === 127)))
        throw new Error("Invalid upstream batch main output identity");
      const identity = typeof entry.output_id === "string" && entry.output_id && entry.output_id.length <= 256 ? entry.output_id : urlKey;
      const byIdentity = identities.get(identity), byURL = urls.get(urlKey);
      if (byIdentity !== undefined && byURL !== undefined && byIdentity !== byURL)
        throw new Error("Ambiguous upstream batch output identity");
      let index = byIdentity === undefined ? byURL : byIdentity;
      if (index === undefined) index = received++;
      else artifact.role = "duplicate";
      // Record both aliases even on a duplicate, so A/x, B/x, B/y remains
      // one logical output rather than counting the final alias a second time.
      identities.set(identity, index);
      urls.set(urlKey, index);
      if (entry.output_index !== undefined) {
        const declaredIndex = entry.output_index;
        if (!Number.isInteger(declaredIndex) || declaredIndex < 0 || declaredIndex >= batch.expected_main_output_count ||
            providerIndices.has(declaredIndex) && providerIndices.get(declaredIndex) !== index ||
            groupIndices.has(index) && groupIndices.get(index) !== declaredIndex)
          throw new Error("Ambiguous upstream batch output index");
        providerIndices.set(declaredIndex, index);
        groupIndices.set(index, declaredIndex);
      }
      artifact.output_id = "main-" + (index + 1);
      artifact.output_index = index;
    }
    for (let i = 0; i < artifacts.length; i++) {
      const artifact = artifacts[i], entry = object(entries[i]) || {};
      if (artifact.role === "main" || artifact.role === "duplicate") continue;
      const declaredIndex = entry.output_index;
      if (declaredIndex !== undefined && (!Number.isInteger(declaredIndex) || declaredIndex < 0 || declaredIndex >= batch.expected_main_output_count))
        throw new Error("Invalid upstream batch auxiliary output index");
      const byIndex = providerIndices.get(declaredIndex);
      if (declaredIndex !== undefined && byIndex === undefined)
        throw new Error("Unknown upstream batch auxiliary output index");
      if (entry.parent_output_id !== undefined && (typeof entry.parent_output_id !== "string" || !entry.parent_output_id || !identities.has(entry.parent_output_id)))
        throw new Error("Unknown upstream batch auxiliary parent identity");
      // A preview's own output_id is not its parent. Only an explicit
      // parent reference from the reviewed output contract can link by ID.
      const byIdentity = typeof entry.parent_output_id === "string" ? identities.get(entry.parent_output_id) : undefined;
      if (byIndex !== undefined && byIdentity !== undefined && byIndex !== byIdentity)
        throw new Error("Ambiguous upstream batch auxiliary output identity");
      const index = byIdentity === undefined ? byIndex : byIdentity;
      if (index !== undefined) { artifact.output_id = "main-" + (index + 1); artifact.output_index = index; }
    }
    const complete = data.is_final && status === "completed";
    const contradictoryFailure = data.is_final && ["failed", "cancelled", "canceled"].includes(status) && received > 0;
    const completeness = received > batch.expected_main_output_count ? "mismatch" : complete ? received === batch.expected_main_output_count ? "complete" : "partial"
      : contradictoryFailure ? received === batch.expected_main_output_count ? "mismatch" : "partial" : "pending";
    data.batch = { received_main_output_count: received, completeness,
      ...(completeness === "partial" ? { failure_reason: "main_outputs_missing" } : completeness === "mismatch" ? { failure_reason: "main_outputs_mismatch" } : {}) };
  }
  return data;
}

function mediaState(ctx) {
  const state = object(ctx.state);
  if (!state || state.mediaGenerationV2 !== true) return null;
  const contract = MEDIA_CONTRACTS.find((model) => model.id === state.gatewayModel);
  if (!contract || contract.kind !== state.kind || contract.native_id !== state.nativeModel ||
      ctx.model !== undefined && ctx.model !== contract.id) throw new Error("Invalid retained media task identity");
  if (state.batch !== undefined) mediaBatch(state.batch);
  return { state, contract };
}

function mediaTaskResult(data) {
  if (data.batch && data.is_final && ["partial", "mismatch"].includes(data.batch.completeness))
    // This is a recognized partial receipt, not a parser failure. Persist the
    // original URLs/counters without completing or settling its reservation.
    return { status: "IN_PROGRESS", progress: "0%", reason: "Upstream native batch main outputs are incomplete" };
  if (!data.is_final && ["pending", "queued", "processing", "running", "needs_review"].includes(data.status))
    return { status: "IN_PROGRESS", progress: "0%" };
  if (data.is_final && data.status === "completed" && !data.artifacts.length) return { status: "UNKNOWN", reason: "Upstream media artifacts are unavailable" };
  return taskResult(data);
}

function snapshot(body, model, expectedId, legacyOutputPolicy = false) {
  if (!object(body)) throw new Error("Invalid upstream task response");
  const id = body.task_id || body.id;
  if (typeof id !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$/.test(id) || (expectedId && id !== expectedId))
    throw new Error("Upstream task identity mismatch");
  const modelMetadata = object(body.model);
  const responseModel = modelMetadata ? modelMetadata.public_model : body.model || body.model_id;
  if (modelMetadata && (typeof responseModel !== "string" || !responseModel)) throw new Error("Invalid upstream task model");
  if (responseModel && responseModel !== model.upstream && responseModel !== model.id) throw new Error("Upstream task model mismatch");
  const status = typeof body.status === "string" ? body.status.toLowerCase() : "unknown";
  const data = { id, kind: model.kind, status, is_final: body.is_final === true,
    outputs: legacyOutputPolicy ? legacyFirstOutput(body, model.kind) : outputs(body, model.kind) };
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
  const v2 = mediaV2(ctx, true);
  if (v2) {
    const outputFormat = v2.request.params.output_format || v2.request.params.format || "";
    const data = mediaSnapshot(response.body, v2.contract, undefined, outputFormat, v2.batch);
    return { taskId: data.id, taskData: data, state: { mediaGenerationV2: true, gatewayModel: v2.contract.id,
      nativeModel: v2.contract.native_id, kind: v2.contract.kind, outputFormat, facts: v2.facts, ...(v2.batch ? { batch: v2.batch } : {}), snapshot: data } };
  }
  const model = modelFor(ctx);
  const data = snapshot(response.body, model);
  const normalized = normalize(ctx.requestBody, model);
  if (model.kind === "audio") data.format = normalized.params.response_format;
  const result = taskResult(data);
  const state = { facts: normalized.facts, kind: model.kind, legacy_outputs_policy_version: 2, snapshot: data };
  if (model.kind === "audio") state.format = normalized.params.response_format;
  // Images must preserve a durable artifact. Audio must run the completion
  // usage hook, including when invoke already returned a ready asset; the
  // conservative output estimate is never a final usage statement.
  return { taskId: data.id, taskData: data, state, ...(model.kind === "video" && ["SUCCESS", "FAILURE"].includes(result.status) ? { immediate: result } : {}) };
}

export function extractUsage(ctx) {
  const v2 = mediaV2(ctx);
  if (v2) return v2.facts;
  return normalize(ctx.requestBody, modelFor(ctx)).facts;
}
export function buildQueryRequest(ctx) {
  const retained = mediaState(ctx);
  if (retained) {
    if (!retained.state.snapshot || retained.state.snapshot.id !== ctx.taskId) throw new Error("Retained task identity mismatch");
    return { url: ctx.baseUrl + "/v1/tasks/" + encodeURIComponent(ctx.taskId), method: "GET", headers: { Authorization: "Bearer " + ctx.apiKey } };
  }
  const model = modelFor(ctx);
  return {
    url: ctx.baseUrl + (model.kind === "video" ? "/v1/tasks/" : "/api/v1/model-runtime/tasks/") + encodeURIComponent(ctx.taskId),
    method: "GET",
    headers: { Authorization: "Bearer " + ctx.apiKey },
  };
}
export function parseTaskResult(ctx, body) {
  const retained = mediaState(ctx);
  if (retained) {
    const data = mediaSnapshot(body, retained.contract, ctx.taskId, retained.state.outputFormat, retained.state.batch);
    return { ...mediaTaskResult(data), state: { ...retained.state, snapshot: data } };
  }
  const state = object(ctx.state) || {};
  if (state.legacy_outputs_policy_version !== undefined && state.legacy_outputs_policy_version !== 2)
    throw new Error("Invalid retained legacy output policy");
  const data = snapshot(body, modelFor(ctx), ctx.taskId, state.legacy_outputs_policy_version === undefined);
  if (data.kind === "audio" && ctx.state) data.format = ctx.state.format;
  // The host owns task.data and stores the raw poll response. Normalized safe
  // output coordinates belong in durable plugin state, not an ignored field.
  return { ...taskResult(data), state: { ...state, snapshot: data } };
}
export function extractUsageOnComplete(ctx, _result, body) {
  if (mediaState(ctx)) return {};
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
  const retained = mediaState(task);
  if (retained) {
    const data = retained.state.snapshot;
    if (task.status !== "SUCCESS" || !data || !Array.isArray(data.artifacts)) return [];
    if (data.artifacts.length > 64) throw new Error("Too many retained media artifacts");
    return data.artifacts.map((entry) => ({ key: entry.key, type: entry.type, mimeType: entry.mimeType }));
  }
  const data = task.state && task.state.snapshot;
  if (task.status !== "SUCCESS" || !data || !data.outputs || !publicURL(data.outputs[0])) return [];
  const kind = data.kind;
  let mimeType = "image/png";
  if (kind === "video") mimeType = "video/mp4";
  if (kind === "audio") mimeType = data.format === "wav" ? "audio/wav" : "audio/mpeg";
  if (data.outputs.length > 64) throw new Error("Too many retained media artifacts");
  return data.outputs.map((_url, index) => ({ key: data.outputs.length === 1 ? kind : kind + "-" + (index + 1), type: kind, mimeType }));
}
export function buildContentRequest(ctx) {
  const retained = mediaState(ctx);
  if (retained) {
    const artifacts = retained.state.snapshot && retained.state.snapshot.artifacts;
    const artifact = Array.isArray(artifacts) && artifacts.find((entry) => entry.key === ctx.artifactKey);
    if (!artifact || !publicURL(artifact.url) || !ctx.clientRequest || !["GET", "HEAD"].includes(ctx.clientRequest.method)) throw new Error("artifact_not_found");
    return { url: artifact.url, method: ctx.clientRequest.method, credentialless: true };
  }
  const data = ctx.state && ctx.state.snapshot;
  const key = typeof ctx.artifactKey === "string" ? ctx.artifactKey : "";
  const index = data && data.outputs && data.outputs.length === 1 ? (key === data.kind ? 0 : -1)
    : data && key.startsWith(data.kind + "-") ? Number(key.slice(data.kind.length + 1)) - 1 : -1;
  const url = data && data.outputs && Number.isInteger(index) && index >= 0 && publicURL(data.outputs[index]);
  if (!url) throw new Error("artifact_not_found");
  return { url, method: ctx.clientRequest.method, credentialless: true };
}
export const protocols = {
  openai_image: {
    decodeRequest: function (ctx) {
      const v2 = mediaV2(ctx);
      if (v2) return decode(ctx, "image");
      const model = modelFor(ctx);
      if (model.kind !== "image") throw new Error("This model does not support image generation");
      const edit = object(model.image_edit);
      const body = decodeImageRequest(ctx, edit ? edit.max_images : 0, edit ? edit.max_bytes : 5242880);
      normalize(body, model);
      return { kind: "submit", model: ctx.model, action: body.reference_images ? "image_edit" : "image", requestBody: body };
    },
    render: function (ctx, _task) {
      const artifacts = object(ctx.artifacts);
      const keys = artifacts && Object.keys(artifacts);
      if (!keys || !keys.length) throw new Error("Image artifact is unavailable");
      if (keys.length > 64) throw new Error("Too many retained media artifacts");
      // Host projection is a map. Multi-result keys carry the original global
      // output position; restore that order instead of relying on map order.
      const position = (key) => Number((/-([1-9][0-9]*)$/.exec(key) || [])[1] || 0);
      keys.sort((a, b) => position(a) - position(b));
      return { data: keys.map((key) => {
        const artifact = object(artifacts[key]);
        if (!artifact || typeof artifact.url !== "string" || !artifact.url) throw new Error("Image artifact is unavailable");
        return { url: artifact.url };
      }) };
    },
  },
  openai_video: {
    decodeRequest: function (ctx) {
      return decode(ctx, "video");
    },
    render: function (_ctx, task) {
      // API v1 TaskView has raw data but no private state or producer version.
      // This single-URL cached projection keeps its historical semantics;
      // accepted new work was already validated by the strict driver hooks.
      return { url: task.data && legacyFirstOutput(task.data, "video")[0] };
    },
  },
};
