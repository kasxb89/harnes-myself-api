// These are the live /v1/models duration policies and documented reference limits.
// The unavailable video-sd25-720p model is intentionally not advertised.
const MODELS = {
  'lec-mj-seedance-2-5-cd-720p': { seconds: [30], defaultSeconds: 30, images: 9, videos: 0, audios: 0 },
  'sd-25-900-2': { seconds: [30], defaultSeconds: 30, images: 9, videos: 0, audios: 0 },
  'k2-sd2.5-720p': { seconds: [30], defaultSeconds: 30, images: 30, videos: 10, audios: 10 },
  'lec-sz-sd2-full-480p': { seconds: [30], defaultSeconds: 30, images: 30, videos: 0, audios: 10 },
  'video-sd25-480p': { min: 15, defaultSeconds: 30, images: 30, videos: 10, audios: 10 },
  'model_5': { min: 15, defaultSeconds: 30, images: 30, videos: 10, audios: 10 },
  'sd2.5-720p-x2': { min: 15, defaultSeconds: 30, images: 30, videos: 10, audios: 10 },
  'sd2-th-2.0-720p': { seconds: [15], defaultSeconds: 15, images: 9, videos: 0, audios: 3 },
  'bt-seedance-2.0': { seconds: [10, 15], defaultSeconds: 15, images: 8, videos: 0, audios: 0 },
  'lec-rs-seedance-2-0-fast': { seconds: [15], defaultSeconds: 15, images: 9, videos: 0, audios: 0 },
  'sd20-933-720p': { seconds: [15], defaultSeconds: 15, images: 9, videos: 3, audios: 3 },
  'sd2-hn2-480-pro': { seconds: [15], defaultSeconds: 15, images: 9, videos: 3, audios: 3 },
  'v3-sd2.0-933-720p': { seconds: [15], defaultSeconds: 15, images: 9, videos: 3, audios: 3 },
  'sd-20-9/3/3-1': { seconds: [15], defaultSeconds: 15, images: 9, videos: 0, audios: 3 },
  'sd-20-903': { seconds: [15], defaultSeconds: 15, images: 9, videos: 0, audios: 3 },
};
const SIZES = ['720x1280', '1280x720', '1024x1792', '1792x1024'];
const FIELDS = ['model', 'prompt', 'seconds', 'duration', 'size', 'gen_type', 'n',
  'images', 'input_reference', 'image_url', 'image_urls', 'videos', 'video_urls', 'audios', 'audio_urls'];
const VIDEO_USAGE_SCHEMA = {
  seconds: { type: 'number', unit: 'second', description: { en: 'Video generation unit price', zh: '视频生成单价' } },
};

export const meta = {
  apiVersion: 1,
  key: 'blackrxai-video',
  name: 'BlackRxAI Seedance',
  icon: 'text:BR',
  version: '1.0.0',
  author: { name: 'Yunshu' },
  description: { en: 'Seedance video generation', zh: 'Seedance 视频生成' },
  baseUrl: 'https://hi.blackrxai.com',
  models: Object.keys(MODELS),
  fetchMode: 'per_task',
  protocols: ['openai_video'],
  usageSchema: VIDEO_USAGE_SCHEMA,
  usageProfiles: Object.keys(MODELS).map(function (model) {
    const config = MODELS[model];
    return { models: [model], schema: VIDEO_USAGE_SCHEMA,
      examples: (config.seconds || [config.min, 30]).map(function (seconds) {
        return { label: String(seconds) + 's', facts: { seconds: seconds } };
      }) };
  }),
};

function record(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function duration(value) {
  if (typeof value !== 'number' && (typeof value !== 'string' || !/^[0-9]+$/.test(value))) {
    throw new Error('seconds must be a supported positive integer');
  }
  const number = Number(value);
  if (!Number.isSafeInteger(number) || number < 1 || number > 30) throw new Error('seconds must be a supported positive integer');
  return number;
}

function policy(model) {
  if (!Object.prototype.hasOwnProperty.call(MODELS, model)) throw new Error('unsupported BlackRxAI video model');
  return MODELS[model];
}

function supportedSeconds(config, seconds) {
  return Number.isSafeInteger(seconds) && seconds > 0 && seconds <= 30 &&
    (config.seconds ? config.seconds.includes(seconds) : seconds >= config.min);
}

function referenceURL(value, media) {
  if (typeof value !== 'string' || !value || /[\s\\]/.test(value)) throw new Error('reference URLs must be HTTP or HTTPS URLs');
  if (media && value.startsWith('/media/') && !value.includes('..') && !value.includes('#')) return value;
  if (!/^https?:\/\/[^/?#]+(?:[/?#].*)?$/.test(value) || value.split('/')[2].includes('@')) {
    throw new Error('reference URLs must be HTTP or HTTPS URLs');
  }
  // The vendor enforces public-address, redirect, content and download limits.
  return value;
}

function urls(value, media) {
  if (value === undefined) return [];
  if (!Array.isArray(value)) throw new Error('reference arrays must contain URL strings');
  return value.map(function (item) { return referenceURL(item, media); });
}

function normalize(ctx) {
  if (!ctx.body || !['json', 'multipart'].includes(ctx.body.kind)) throw new Error('JSON or multipart body required');
  const multipart = ctx.body.kind === 'multipart';
  const raw = multipart ? ctx.body.fields || {} : ctx.body.value;
  if (!record(raw)) throw new Error('request body must be an object');
  const req = {};
  for (const name of Object.keys(raw)) {
    if (!FIELDS.includes(name)) throw new Error('unsupported video field: ' + name);
    if (multipart) {
      if (!Array.isArray(raw[name])) throw new Error('invalid multipart field');
      if (['image_urls', 'video_urls', 'audio_urls'].includes(name)) req[name] = raw[name];
      else {
        if (raw[name].length !== 1) throw new Error(name + ' must be provided once');
        req[name] = raw[name][0];
      }
    } else req[name] = raw[name];
  }
  const config = policy(ctx.upstreamModel || ctx.model);
  if (req.model !== undefined && req.model !== ctx.model) throw new Error('model does not match the selected model');
  if (typeof req.prompt !== 'string' || !req.prompt.trim()) throw new Error('prompt is required');
  if (req.n !== undefined && req.n !== 1 && req.n !== '1') throw new Error('only one video per task is supported');
  const seconds = req.seconds === undefined ? (req.duration === undefined ? config.defaultSeconds : duration(req.duration)) : duration(req.seconds);
  if (req.duration !== undefined && duration(req.duration) !== seconds) throw new Error('seconds and duration must agree');
  if (!supportedSeconds(config, seconds)) throw new Error('seconds is not supported by the selected model');
  if (req.size !== undefined && !SIZES.includes(req.size)) throw new Error('unsupported video size');
  const images = [];
  if (req.image_url !== undefined) images.push(referenceURL(req.image_url, false));
  images.push(...urls(req.image_urls, false));
  if (!multipart) images.push(...urls(req.images, false));
  else if (req.images !== undefined || req.videos !== undefined || req.audios !== undefined || req.input_reference !== undefined) {
    throw new Error('multipart reference material must be a file or a URL field');
  }
  if (!multipart && req.input_reference !== undefined) throw new Error('input_reference requires a multipart file');
  const videos = urls(req.video_urls, true).concat(multipart ? [] : urls(req.videos, true));
  const audios = urls(req.audio_urls, true).concat(multipart ? [] : urls(req.audios, true));
  const files = [];
  const counts = { images: images.length, videos: videos.length, audios: audios.length };
  const orderedFiles = (ctx.body.files || []).filter(function (file) { return file.field === 'input_reference'; })
    .concat((ctx.body.files || []).filter(function (file) { return file.field !== 'input_reference'; }));
  let inputReferences = 0;
  for (const file of orderedFiles) {
    const name = file.field === 'input_reference' ? 'images' : file.field;
    if (!['images', 'videos', 'audios'].includes(name)) throw new Error('unsupported reference file field');
    if (file.field === 'input_reference' && ++inputReferences > 1) throw new Error('input_reference must be provided once');
    if (name === 'videos' && !/\.(mp4|mov|webm|m4v)$/i.test(file.filename)) throw new Error('unsupported reference video format');
    if (name === 'audios' && !/\.(mp3|m4a|wav|aac|flac|ogg)$/i.test(file.filename)) throw new Error('unsupported reference audio format');
    counts[name]++;
    files.push({ name: name, fileRef: file.ref, filename: file.filename });
  }
  for (const name of ['images', 'videos', 'audios']) {
    if (counts[name] > config[name]) throw new Error('reference ' + name + ' exceed the selected model limit');
  }
  const hasReferences = counts.images + counts.videos + counts.audios > 0;
  const genType = hasReferences ? 'reference_to_video' : 'text_to_video';
  if (req.gen_type !== undefined && req.gen_type !== genType) throw new Error('gen_type conflicts with reference material');
  const body = { model: ctx.model, prompt: req.prompt, seconds: seconds, gen_type: genType,
    image_urls: images, video_urls: videos, audio_urls: audios, referenceFiles: files };
  if (req.size !== undefined) body.size = req.size;
  return { kind: 'submit', model: ctx.model, action: genType, requestBody: body };
}

export function buildSubmitRequest(ctx) {
  const req = ctx.requestBody;
  const model = ctx.upstreamModel || ctx.model;
  const config = policy(model);
  if (!supportedSeconds(config, req.seconds)) throw new Error('seconds is not supported by the selected upstream model');
  for (const name of ['images', 'videos', 'audios']) {
    const urlName = { images: 'image_urls', videos: 'video_urls', audios: 'audio_urls' }[name];
    const count = req[urlName].length + req.referenceFiles.filter(function (file) { return file.name === name; }).length;
    if (count > config[name]) throw new Error('reference ' + name + ' exceed the selected upstream model limit');
  }
  if (typeof ctx.publicTaskId !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(ctx.publicTaskId)) throw new Error('gateway task ID is required');
  const headers = { Authorization: 'Bearer ' + ctx.apiKey, Accept: 'application/json', 'Idempotency-Key': ctx.publicTaskId };
  const body = { model: model, prompt: req.prompt, seconds: req.seconds, gen_type: req.gen_type };
  if (req.size !== undefined) body.size = req.size;
  if (req.referenceFiles.length) {
    const parts = Object.keys(body).map(function (name) { return { name: name, value: String(body[name]) }; });
    for (const name of ['image_urls', 'video_urls', 'audio_urls']) {
      for (const value of req[name]) parts.push({ name: name, value: value });
    }
    for (const file of req.referenceFiles) parts.push({ name: file.name, fileRef: file.fileRef, filename: file.filename });
    return { url: ctx.baseUrl + '/v1/videos', method: 'POST', headers: headers, bodyType: 'multipart', parts: parts };
  }
  for (const name of ['image_urls', 'video_urls', 'audio_urls']) if (req[name].length) body[name] = req[name];
  headers['Content-Type'] = 'application/json';
  return { url: ctx.baseUrl + '/v1/videos', method: 'POST', headers: headers, body: body };
}

export function extractUsage(ctx) {
  const seconds = ctx.requestBody.seconds;
  if (!supportedSeconds(policy(ctx.upstreamModel || ctx.model), seconds)) throw new Error('invalid video billing duration');
  return { seconds: seconds };
}

// This provider charges the selected duration/tariff, not measured output length.
// Leaving completion facts absent preserves the validated frozen request facts.
export function extractUsageOnComplete() { return {}; }

export function parseSubmitResponse(ctx, response) {
  const body = response.body;
  if (!record(body) || typeof body.id !== 'string' || !body.id.trim()) throw new Error('upstream did not return a video task');
  const parsed = parseTaskResult({ taskId: body.id }, body);
  // A successful HTTP acceptance with a task ID must stay durable even if the
  // initial status is absent/new. Query parsing still returns UNKNOWN until a
  // recognized state arrives; do not lose the receipt or submit it again.
  const result = { taskId: body.id, taskData: body };
  if (parsed.status === 'SUCCESS' || parsed.status === 'FAILURE') result.immediate = parsed;
  return result;
}

export function buildQueryRequest(ctx) {
  return { url: ctx.baseUrl + '/v1/videos/' + encodeURIComponent(ctx.taskId), method: 'GET',
    headers: { Authorization: 'Bearer ' + ctx.apiKey, Accept: 'application/json' } };
}

export function parseTaskResult(ctx, body) {
  if (!record(body) || body.id !== ctx.taskId) return { status: 'UNKNOWN', reason: 'unrecognized video task' };
  const statuses = { queued: 'QUEUED', in_progress: 'IN_PROGRESS', completed: 'SUCCESS', failed: 'FAILURE', cancelled: 'FAILURE' };
  const status = Object.prototype.hasOwnProperty.call(statuses, body.status) ? statuses[body.status] : undefined;
  if (!status) return { status: 'UNKNOWN', reason: 'unrecognized video status' };
  const value = Number(body.progress);
  const progress = status === 'SUCCESS' || status === 'FAILURE' ? '100%' :
    (Number.isFinite(value) ? Math.max(0, Math.min(100, value)) : 0) + '%';
  const result = { status: status, progress: progress };
  if (status === 'FAILURE') result.reason = record(body.error) && typeof body.error.message === 'string' ? body.error.message : 'video generation failed';
  return result;
}

export function listArtifacts(task) {
  return task.status === 'SUCCESS' ? [{ key: 'video', type: 'video', mimeType: 'video/mp4' }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== 'video') throw new Error('artifact_not_found');
  return { url: ctx.baseUrl + '/v1/videos/' + encodeURIComponent(ctx.upstreamTaskId) + '/content',
    method: ctx.clientRequest.method, headers: { Authorization: 'Bearer ' + ctx.apiKey } };
}

export const protocols = { openai_video: {
  decodeRequest: normalize,
  render(ctx, task) {
    const data = record(task.data) ? task.data : {};
    const output = { id: task.task_id, object: 'video' };
    // TaskView intentionally omits private model properties. Only echo a duration
    // when the upstream snapshot itself identifies one of our supported tariffs.
    const config = Object.prototype.hasOwnProperty.call(MODELS, data.model) ? MODELS[data.model] : undefined;
    if (config && supportedSeconds(config, data.seconds)) output.seconds = data.seconds;
    if (SIZES.includes(data.size)) output.size = data.size;
    if (task.status === 'FAILURE') output.error = { code: 'video_generation_failed', message: 'The video generation task failed.' };
    return output;
  },
} };
