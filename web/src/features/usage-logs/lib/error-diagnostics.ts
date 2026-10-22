/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { LogOtherData } from '../types'

export type ErrorReason =
  | 'upstream_balance'
  | 'user_balance'
  | 'token_quota'
  | 'authentication'
  | 'permission'
  | 'rate_limit'
  | 'concurrency_limit'
  | 'generation_capacity'
  | 'model_unavailable'
  | 'invalid_request'
  | 'context_length'
  | 'content_policy'
  | 'payload_too_large'
  | 'unsupported_operation'
  | 'no_available_channel'
  | 'timeout'
  | 'connection'
  | 'dns'
  | 'tls'
  | 'upstream_unavailable'
  | 'server_error'
  | 'invalid_response'
  | 'payment_required'
  | 'not_found'
  | 'channel_configuration'
  | 'billing_configuration'
  | 'request_conversion'
  | 'internal_error'
  | 'request_cancelled'
  | 'response_read_failed'
  | 'token_count_failed'
  | 'unknown'

export interface ErrorDiagnostics {
  message: string
  type?: string
  code?: string
  reason: ErrorReason
  source: 'upstream' | 'gateway' | 'unknown'
  upstream: boolean
  balanceInsufficient: boolean
}

// Only log metadata, never provider text, can establish these local failures.
// Some public codes overlap with providers and need the source checks below.
const gatewayReasons: Readonly<Record<string, ErrorReason>> = {
  insufficient_user_quota: 'user_balance',
  pre_consume_token_quota_failed: 'token_quota',
  invalid_request: 'invalid_request',
  bad_request_body: 'invalid_request',
  read_request_body_failed: 'invalid_request',
  sensitive_words_detected: 'content_policy',
  prompt_blocked: 'content_policy',
  access_denied: 'permission',
  'channel:no_available_key': 'no_available_channel',
  get_channel_failed: 'no_available_channel',
  'channel:invalid_key': 'channel_configuration',
  'channel:param_override_invalid': 'channel_configuration',
  'channel:header_override_invalid': 'channel_configuration',
  'channel:model_mapped_error': 'channel_configuration',
  'channel:aws_client_error': 'channel_configuration',
  invalid_api_type: 'channel_configuration',
  model_price_error: 'billing_configuration',
  count_token_failed: 'token_count_failed',
  json_marshal_failed: 'request_conversion',
  convert_request_failed: 'request_conversion',
  gen_relay_info_failed: 'internal_error',
  query_data_error: 'internal_error',
  update_data_error: 'internal_error',
  read_response_body_failed: 'response_read_failed',
  bad_response_body: 'invalid_response',
  empty_response: 'invalid_response',
  bad_response: 'invalid_response',
  'channel:response_time_exceeded': 'timeout',
}

const providerReasons: Readonly<Record<string, ErrorReason>> = {
  insufficient_balance_error: 'upstream_balance',
  insufficient_balance: 'upstream_balance',
  insufficient_quota: 'upstream_balance',
  insufficient_credits: 'upstream_balance',
  credit_balance_too_low: 'upstream_balance',
  authentication_error: 'authentication',
  invalid_api_key: 'authentication',
  invalid_authentication: 'authentication',
  authentication_failed: 'authentication',
  unauthorized: 'authentication',
  invalid_token: 'authentication',
  token_expired: 'authentication',
  permission_error: 'permission',
  permission_denied: 'permission',
  forbidden: 'permission',
  access_denied: 'permission',
  rate_limit_error: 'rate_limit',
  rate_limit_exceeded: 'rate_limit',
  too_many_requests: 'rate_limit',
  concurrency_limit_exceeded: 'concurrency_limit',
  concurrent_limit_exceeded: 'concurrency_limit',
  too_many_concurrent_requests: 'concurrency_limit',
  generation_resources_unavailable: 'generation_capacity',
  resource_exhausted: 'generation_capacity',
  model_not_found: 'model_unavailable',
  model_not_available: 'model_unavailable',
  model_unavailable: 'model_unavailable',
  unsupported_model: 'model_unavailable',
  invalid_request_error: 'invalid_request',
  invalid_request: 'invalid_request',
  invalid_argument: 'invalid_request',
  invalid_parameter: 'invalid_request',
  invalid_params: 'invalid_request',
  validation_error: 'invalid_request',
  context_length_exceeded: 'context_length',
  context_window_exceeded: 'context_length',
  prompt_too_long: 'context_length',
  content_policy_violation: 'content_policy',
  content_filter: 'content_policy',
  content_filter_error: 'content_policy',
  moderation_blocked: 'content_policy',
  safety_violation: 'content_policy',
  responsible_ai_policy_violation: 'content_policy',
  request_too_large: 'payload_too_large',
  payload_too_large: 'payload_too_large',
  request_entity_too_large: 'payload_too_large',
  unsupported_operation: 'unsupported_operation',
  unsupported_feature: 'unsupported_operation',
  method_not_allowed: 'unsupported_operation',
  not_implemented: 'unsupported_operation',
  request_timeout: 'timeout',
  timeout: 'timeout',
  upstream_timeout: 'timeout',
  gateway_timeout: 'timeout',
  connection_error: 'connection',
  dns_error: 'dns',
  tls_error: 'tls',
  ssl_error: 'tls',
  server_error: 'upstream_unavailable',
  internal_server_error: 'upstream_unavailable',
  service_unavailable: 'upstream_unavailable',
  overloaded_error: 'upstream_unavailable',
  provider_down: 'upstream_unavailable',
  invalid_response: 'invalid_response',
  payment_required: 'payment_required',
  not_found: 'not_found',
}

interface ErrorEnvelope {
  message?: string
  type?: string
  code?: string
  providerEnvelope: boolean
  malformed: boolean
  json: boolean
}

type ErrorMetadata = Pick<
  LogOtherData,
  | 'error_code'
  | 'error_type'
  | 'status_code'
  | 'status_code_origin'
  | 'upstream_http_status'
  | 'provider_error_code'
>

export function getErrorHttpStatus(other: ErrorMetadata | null): {
  recorded: number | undefined
  mapped: boolean
  upstream: number | undefined
} {
  const observed = other?.upstream_http_status
  const upstream =
    typeof observed === 'number' &&
    Number.isInteger(observed) &&
    observed >= 100 &&
    observed <= 599
      ? observed
      : undefined
  const mapped =
    other?.status_code_origin === 'gateway_task_mapping' ||
    (other?.status_code_origin !== 'upstream_http' &&
      other?.error_type === 'task_error' &&
      (other.error_code === 'task_failed' ||
        other.error_code === 'task_timeout'))
  return { recorded: other?.status_code, mapped, upstream }
}

function readErrorEnvelope(content: string): ErrorEnvelope {
  let payload: unknown
  try {
    payload = JSON.parse(content)
  } catch {
    return {
      providerEnvelope: false,
      malformed: /^[{[]/.test(content),
      json: false,
    }
  }
  if (
    typeof payload !== 'object' ||
    payload === null ||
    Array.isArray(payload)
  ) {
    return { providerEnvelope: false, malformed: false, json: true }
  }

  const envelope = payload as Record<string, unknown>
  let detail = envelope
  if (
    typeof envelope.error === 'object' &&
    envelope.error !== null &&
    !Array.isArray(envelope.error)
  ) {
    detail = envelope.error as Record<string, unknown>
  }
  let message: unknown =
    detail.message ?? detail.detail ?? envelope.message ?? envelope.detail
  if (typeof envelope.error === 'string') message = envelope.error
  let code: unknown = detail.code
  const type = typeof detail.type === 'string' ? detail.type : undefined
  let providerEnvelope = false
  if (
    typeof envelope.base_resp === 'object' &&
    envelope.base_resp !== null &&
    !Array.isArray(envelope.base_resp)
  ) {
    const base = envelope.base_resp as Record<string, unknown>
    if (typeof base.status_msg === 'string') {
      message = base.status_msg
      code = base.status_code
      providerEnvelope = true
    }
  }
  const normalizedCode =
    typeof code === 'string' || typeof code === 'number'
      ? String(code)
      : undefined
  // Generic bad_response_status_code and status prefixes also wrap local task
  // errors. Require a recognizable provider error in addition to that metadata.
  providerEnvelope ||= Boolean(
    type &&
    Object.hasOwn(providerReasons, type) &&
    !Object.hasOwn(gatewayReasons, type)
  )
  providerEnvelope ||= Boolean(
    normalizedCode &&
    Object.hasOwn(providerReasons, normalizedCode) &&
    !Object.hasOwn(gatewayReasons, normalizedCode)
  )
  return {
    message:
      typeof message === 'string' && message.trim() ? message : undefined,
    type: type?.trim() ? type : undefined,
    code: normalizedCode?.trim() ? normalizedCode : undefined,
    providerEnvelope,
    malformed: false,
    json: true,
  }
}

function transportReason(message: string): ErrorReason | undefined {
  if (
    /context cancel(?:ed|led)|request (?:was )?cancel(?:ed|led)|请求已取消/i.test(
      message
    )
  ) {
    return 'request_cancelled'
  }
  if (
    /no such host|server misbehaving|temporary failure in name resolution|name or service not known|DNS (?:lookup|resolution).*(?:fail|error)/i.test(
      message
    )
  ) {
    return 'dns'
  }
  if (
    /x509:|tls:|TLS handshake|SSL certificate|certificate (?:has expired|signed by unknown authority|verify failed|is not valid)/i.test(
      message
    )
  ) {
    return 'tls'
  }
  if (
    /context deadline exceeded|Client[.]Timeout|i\/o timeout|timed out|request timeout|timeout awaiting response headers|请求超时/i.test(
      message
    )
  ) {
    return 'timeout'
  }
  if (
    /connection (?:refused|reset|closed|aborted)|network is unreachable|no route to host|unexpected EOF|(?:^|[ :])EOF$/i.test(
      message
    )
  ) {
    return 'connection'
  }
  return undefined
}

function messageReason(
  message: string,
  upstream: boolean
): ErrorReason | undefined {
  const transport = transportReason(message)
  if (transport) return transport
  if (
    upstream &&
    /insufficient (?:account )?(?:balance|credits|quota)|credit balance.*too low|(?:余额|积分|额度)(?:不足|耗尽)/i.test(
      message
    )
  ) {
    return 'upstream_balance'
  }
  if (
    /too many concurrent requests|concurrency limit|concurrent (?:request )?limit|并发(?:请求|数)?.*(?:超限|超过|限制)/i.test(
      message
    )
  ) {
    return 'concurrency_limit'
  }
  if (
    /rate[ _-]?limit(?:ed| exceeded| reached)|too many requests|请求(?:频率|速率).*(?:超过|超出|限制)|触发限流/i.test(
      message
    )
  ) {
    return 'rate_limit'
  }
  if (
    /maximum context length|context (?:length|window).*(?:exceed|limit)|prompt (?:is )?too long|上下文.*(?:超长|超过|超出)/i.test(
      message
    )
  ) {
    return 'context_length'
  }
  if (
    /content policy violation|violat(?:es|ion).*content policy|content (?:was )?(?:blocked|filtered)|blocked by.*safety|内容.*(?:安全策略|审核不通过|未通过安全审核)|被内容策略拒绝/i.test(
      message
    )
  ) {
    return 'content_policy'
  }
  if (
    /没有可用的生成资源|no (?:available )?generation resources|generation resources (?:are )?unavailable/i.test(
      message
    )
  ) {
    return 'generation_capacity'
  }
  if (
    /invalid api[ _-]?key|incorrect api[ _-]?key|authentication (?:failed|required)|unauthori[sz]ed|API key (?:is )?(?:invalid|expired|missing)/i.test(
      message
    )
  ) {
    return 'authentication'
  }
  if (
    /permission denied|access denied|does not have access|insufficient permissions|权限不足|无权访问/i.test(
      message
    )
  ) {
    return 'permission'
  }
  if (
    /model.*(?:does not exist|not found|not available|is unavailable)|模型.*(?:不存在|不可用|不支持)/i.test(
      message
    )
  ) {
    return 'model_unavailable'
  }
  if (
    /request (?:body|entity|payload).*(?:too large|exceed.*size)|payload too large|请求体.*(?:过大|超过.*限制)/i.test(
      message
    )
  ) {
    return 'payload_too_large'
  }
  return undefined
}

function httpReason(
  status: number | undefined,
  upstream: boolean
): ErrorReason | undefined {
  switch (status) {
    case 400:
    case 415:
    case 422:
      return 'invalid_request'
    case 401:
      return 'authentication'
    case 402:
      return 'payment_required'
    case 403:
      return 'permission'
    case 404:
    case 410:
      return 'not_found'
    case 405:
    case 501:
      return 'unsupported_operation'
    case 408:
    case 504:
      return 'timeout'
    case 413:
      return 'payload_too_large'
    case 429:
      return 'rate_limit'
    case 500:
    case 502:
    case 503:
      return upstream ? 'upstream_unavailable' : 'server_error'
    default:
      return upstream && status !== undefined && status >= 500 && status <= 599
        ? 'upstream_unavailable'
        : undefined
  }
}

export function parseErrorDiagnostics(
  content: string,
  other: ErrorMetadata | null
): ErrorDiagnostics | null {
  const trimmedContent = content.trim()
  const prefix = /^status_code=(\d{3}),\s*/.exec(trimmedContent)
  const body = prefix ? trimmedContent.slice(prefix[0].length) : trimmedContent
  const envelope = readErrorEnvelope(body)
  const metadataCode = other?.error_code
  const metadataType = other?.error_type
  const status = other?.status_code || (prefix ? Number(prefix[1]) : undefined)
  if (!trimmedContent && !metadataCode && !metadataType && !status) return null

  const diagnostics: ErrorDiagnostics = {
    message: envelope.message ?? body,
    reason: 'unknown',
    source: 'unknown',
    upstream: false,
    balanceInsufficient: false,
  }
  if (envelope.type) diagnostics.type = envelope.type
  if (envelope.code) diagnostics.code = envelope.code

  const providerErrorType =
    metadataType === 'openai_error' || metadataType === 'claude_error'
  if (providerErrorType && metadataCode === 'insufficient_user_quota') {
    // Both a local TaskError and an upstream OpenAI error can retain this code.
    // The stored fields do not distinguish whose balance was checked.
    return diagnostics
  }
  if (providerErrorType && metadataCode === 'pre_consume_token_quota_failed') {
    diagnostics.reason = 'token_quota'
    return diagnostics
  }

  const sharedLocalCode =
    metadataCode === 'invalid_request' || metadataCode === 'access_denied'
  let localReason: ErrorReason | undefined
  if (
    metadataCode &&
    Object.hasOwn(gatewayReasons, metadataCode) &&
    (!sharedLocalCode || !metadataType || metadataType === 'new_api_error')
  ) {
    localReason = gatewayReasons[metadataCode]
  }
  if (metadataCode === 'model_not_found' && metadataType === 'new_api_error') {
    localReason = 'no_available_channel'
  }
  if (metadataCode === 'do_request_failed') {
    localReason = transportReason(diagnostics.message) ?? 'connection'
  }
  if (localReason) {
    diagnostics.reason = localReason
    diagnostics.source = 'gateway'
    return diagnostics
  }

  if (metadataType === 'task_error') {
    if (metadataCode === 'task_timeout') {
      diagnostics.reason = 'timeout'
      diagnostics.source = 'gateway'
    } else if (metadataCode === 'transport_error') {
      diagnostics.reason = transportReason(diagnostics.message) ?? 'connection'
      diagnostics.source = 'gateway'
    } else if (
      metadataCode === 'hook_error' ||
      metadataCode === 'unrecognized'
    ) {
      diagnostics.reason = 'invalid_response'
      if (metadataCode === 'hook_error') diagnostics.source = 'gateway'
    } else if (
      (metadataCode === 'not_found' && (status === 404 || status === 410)) ||
      (metadataCode === 'auth' && (status === 401 || status === 403)) ||
      (metadataCode === 'transient' &&
        (status === 429 ||
          (status !== undefined && status >= 500 && status <= 599)))
    ) {
      diagnostics.reason = httpReason(status, true) ?? 'unknown'
      diagnostics.source = 'upstream'
      diagnostics.upstream = true
    }
    if (diagnostics.reason !== 'unknown') return diagnostics
    if (
      metadataCode === 'task_failed' &&
      other?.status_code_origin === 'gateway_task_mapping'
    ) {
      const observedResponse = getErrorHttpStatus(other).upstream !== undefined
      const providerCode = other.provider_error_code
      // Immediate task results lack an HTTP status. The gateway's normalized
      // business codes still describe the rejection without proving its source.
      const normalizedTaskCode =
        providerCode === 'content_policy_violation' ||
        providerCode === 'resource_exhausted' ||
        providerCode === 'rate_limit_exceeded' ||
        providerCode === 'insufficient_quota'
      const providerReason =
        providerCode &&
        (observedResponse || normalizedTaskCode) &&
        Object.hasOwn(providerReasons, providerCode)
          ? providerReasons[providerCode]
          : undefined
      if (observedResponse || providerReason) {
        if (observedResponse) {
          diagnostics.upstream = true
          diagnostics.source = 'upstream'
        }
        diagnostics.reason =
          providerReason ??
          messageReason(diagnostics.message, true) ??
          'unknown'
        if (providerReason) diagnostics.code = providerCode
        diagnostics.balanceInsufficient =
          diagnostics.reason === 'upstream_balance'
        return diagnostics
      }
    }
  }

  // The standard relay stores provider error.code in metadata and only its
  // message in content. Public codes shared with local errors prove no source.
  let metadataProviderCode: string | undefined
  if (
    providerErrorType &&
    metadataCode &&
    Object.hasOwn(providerReasons, metadataCode)
  ) {
    metadataProviderCode = metadataCode
  }
  diagnostics.upstream =
    (metadataCode === 'bad_response_status_code' &&
      envelope.providerEnvelope) ||
    Boolean(
      metadataProviderCode &&
      !Object.hasOwn(gatewayReasons, metadataProviderCode)
    )
  if (diagnostics.upstream) diagnostics.source = 'upstream'
  let structuredReason: ErrorReason | undefined
  let genericInvalidRequest = false
  if (envelope.code && Object.hasOwn(providerReasons, envelope.code)) {
    structuredReason = providerReasons[envelope.code]
    genericInvalidRequest = envelope.code === 'invalid_request_error'
  } else if (metadataProviderCode) {
    structuredReason = providerReasons[metadataProviderCode]
    genericInvalidRequest = metadataProviderCode === 'invalid_request_error'
  }
  if (
    !structuredReason &&
    envelope.type &&
    Object.hasOwn(providerReasons, envelope.type)
  ) {
    structuredReason = providerReasons[envelope.type]
    genericInvalidRequest = envelope.type === 'invalid_request_error'
  }
  if (
    (structuredReason === 'upstream_balance' ||
      structuredReason === 'upstream_unavailable') &&
    !diagnostics.upstream
  ) {
    structuredReason = undefined
  }

  const invalidBody =
    envelope.malformed ||
    /^<(?:!doctype html|html|head|body)\b/i.test(body) ||
    Boolean(prefix && !body)
  const textReason =
    !envelope.json || envelope.message
      ? messageReason(diagnostics.message, diagnostics.upstream)
      : undefined
  if (
    genericInvalidRequest &&
    (textReason === 'upstream_balance' ||
      textReason === 'context_length' ||
      textReason === 'model_unavailable' ||
      textReason === 'content_policy' ||
      textReason === 'generation_capacity')
  ) {
    structuredReason = textReason
  }
  if (structuredReason) diagnostics.reason = structuredReason
  else if (invalidBody) diagnostics.reason = 'invalid_response'
  else {
    const statusReason =
      metadataType === 'task_error' && metadataCode === 'task_failed'
        ? undefined
        : httpReason(status, diagnostics.upstream)
    diagnostics.reason = textReason ?? statusReason ?? 'unknown'
  }
  diagnostics.balanceInsufficient = diagnostics.reason === 'upstream_balance'
  return diagnostics
}
