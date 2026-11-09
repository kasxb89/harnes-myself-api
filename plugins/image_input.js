// Shared OpenAI image ingress. This is embedded into each immutable supplier
// plugin source; only host-issued file references may become upload parts.
function imageReference(value, maxBytes, allowFile) {
  if (allowFile && value && typeof value === "object" && !Array.isArray(value)) {
    if (Object.keys(value).some((key) => !["__fileRef", "encoding", "mimeType", "maxBytes"].includes(key)) ||
        typeof value.__fileRef !== "string" || !value.__fileRef.startsWith("request_file:") ||
        value.encoding !== "dataUrl" || !["image/png", "image/jpeg", "image/webp"].includes(value.mimeType) || value.maxBytes !== maxBytes)
      throw new Error("Invalid image upload reference");
    return value;
  }
  if (typeof value !== "string" || !value || value !== value.trim()) throw new Error("Invalid reference image");
  if (value.startsWith("data:")) {
    const match = /^data:(image\/(?:png|jpeg|webp));base64,([A-Za-z0-9+/]+={0,2})$/.exec(value);
    if (!match || match[2].length % 4 !== 0 || match[2].length / 4 * 3 - (match[2].endsWith("==") ? 2 : match[2].endsWith("=") ? 1 : 0) > maxBytes)
      throw new Error("Reference image exceeds its size limit or has an unsupported encoding");
    return value;
  }
  if (value.length > 8192 || !/^https:\/\/[A-Za-z0-9][A-Za-z0-9.-]*(?::443)?(?:\/[^\s\\<>"']*)?$/.test(value))
    throw new Error("Reference images require a public HTTPS URL or image data URL");
  return value;
}

function imageReferences(body, maxImages, maxBytes) {
  const refs = body.reference_images === undefined ? [] : body.reference_images;
  if (!Array.isArray(refs) || refs.length > maxImages) throw new Error("Reference image count is not supported by this model");
  return refs.map((value) => imageReference(value, maxBytes, true));
}

function decodeImageRequest(ctx, maxImages, maxBytes) {
  if (!["generate", "edit"].includes(ctx.operation) || !ctx.body) throw new Error("Unsupported image operation");
  let body;
  if (ctx.body.kind === "json") {
    const raw = ctx.body.value;
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw new Error("An image request object is required");
    body = { ...raw };
    const carriers = ["image", "reference_images"].filter((key) => body[key] !== undefined);
    if (carriers.length > 1) throw new Error("Conflicting reference image fields");
    const value = carriers.length ? body[carriers[0]] : [];
    const refs = Array.isArray(value) ? value : [value];
    body.reference_images = refs.map((ref) => imageReference(ref, maxBytes, false));
    delete body.image;
  } else if (ctx.body.kind === "multipart" && ctx.operation === "edit") {
    body = {};
    const refs = [];
    for (const [name, values] of Object.entries(ctx.body.fields || {})) {
      if (!Array.isArray(values)) throw new Error("Invalid multipart image fields");
      if (name === "image" || name === "image[]" || name === "reference_images") {
        refs.push(...values.map((ref) => imageReference(ref, maxBytes, false)));
      } else {
        if (values.length !== 1) throw new Error("Duplicate image parameter: " + name);
        if (["n", "count"].includes(name)) {
          if (values[0] !== "1") throw new Error("Only one output image is supported");
          body[name] = 1;
        } else if (name === "stream") {
          if (values[0] !== "false") throw new Error("Image streaming is not supported");
          body[name] = false;
        } else body[name] = values[0];
      }
    }
    for (const file of ctx.body.files || []) {
      const mime = typeof file.mimeType === "string" ? file.mimeType.toLowerCase() : "";
      if (!["image", "image[]"].includes(file.field) || typeof file.ref !== "string" || !file.ref.startsWith("request_file:") ||
          !["image/png", "image/jpeg", "image/webp"].includes(mime) || !Number.isSafeInteger(file.size) || file.size <= 0 || file.size > maxBytes)
        throw new Error("Unsupported reference image upload");
      refs.push({ __fileRef: file.ref, encoding: "dataUrl", mimeType: mime, maxBytes });
    }
    body.reference_images = refs;
  } else throw new Error("JSON or multipart image input is required");
  if (body.model !== undefined && body.model !== ctx.model) throw new Error("Image model differs from the selected route");
  body.model = ctx.model;
  const refs = imageReferences(body, maxImages, maxBytes);
  if (ctx.operation === "edit" && refs.length === 0) throw new Error("An image edit requires a source image");
  // Preserve legacy text-only requests without introducing an optional field.
  if (refs.length === 0) delete body.reference_images;
  else body.reference_images = refs;
  return body;
}
