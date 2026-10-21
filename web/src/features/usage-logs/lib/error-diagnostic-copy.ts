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
import type { TFunction } from 'i18next'

import type { ErrorReason } from './error-diagnostics'

const errorDiagnosticKeys = {
  upstream_balance: {
    title: 'Upstream account balance insufficient',
    description:
      "The channel's upstream account has insufficient credit. Top up that account or use another funded channel.",
  },
  user_balance: {
    title: 'User account balance or plan quota insufficient',
    description:
      'The user balance or plan quota is insufficient. Check the user account and the quota required for this request.',
  },
  token_quota: {
    title: 'Token quota check or precharge failed',
    description:
      'The token quota check or precharge did not pass. Check token limits, account plans and the original error; this alone does not establish insufficient user funds.',
  },
  authentication: {
    title: 'Authentication failed',
    description:
      'Credentials failed authentication. Check that the API key is valid and that the request uses the required authentication headers.',
  },
  permission: {
    title: 'Request denied',
    description:
      'The request is not authorized. Check account, token, group and model access, and the restrictions stated in the original response.',
  },
  rate_limit: {
    title: 'Request rate limit exceeded',
    description:
      'Request frequency or usage reached a limit. Follow the limits and waiting period in the original response and reduce the request rate.',
  },
  concurrency_limit: {
    title: 'Concurrent request limit exceeded',
    description:
      'Too many requests are being processed at once. Reduce concurrency and wait for existing requests to finish.',
  },
  model_unavailable: {
    title: 'Model does not exist or is unavailable',
    description:
      'The requested model is missing or unavailable. Check the exact model name, mapping and access against the original response.',
  },
  invalid_request: {
    title: 'Invalid request parameters',
    description:
      'The request failed parameter validation. Check required fields, types, allowed values and endpoint format against the original response.',
  },
  context_length: {
    title: 'Model context limit exceeded',
    description:
      'The input and reserved output exceed the model context limit. Shorten the context or reduce the output length.',
  },
  content_policy: {
    title: 'Content safety policy triggered',
    description:
      'The request or generated content triggered a safety rule. Review prompts, images and other input using the original response.',
  },
  payload_too_large: {
    title: 'Request body or file too large',
    description:
      'The request body or uploaded file exceeds an endpoint limit. Reduce its size and check file size, dimensions and format requirements.',
  },
  unsupported_operation: {
    title: 'Endpoint or operation unsupported',
    description:
      'The endpoint, method or operation is unsupported. Check the request path, HTTP method and model capabilities.',
  },
  no_available_channel: {
    title: 'No available channel',
    description:
      'The gateway found no usable channel for this request. Check group routing, model availability and channel status.',
  },
  timeout: {
    title: 'Request timed out',
    description:
      'The request did not complete within the time limit. Check the original task or acceptance status before deciding whether to submit again.',
  },
  connection: {
    title: 'Network connection failed',
    description:
      'A network connection failed or was interrupted. Check connectivity and proxy settings, and confirm the original request acceptance status before resubmitting.',
  },
  dns: {
    title: 'DNS resolution failed',
    description:
      'The service hostname could not be resolved. Check the channel hostname, DNS settings and network access.',
  },
  tls: {
    title: 'TLS certificate or handshake failed',
    description:
      'TLS validation or negotiation failed. Check the service certificate, hostname, system time and proxy configuration.',
  },
  upstream_unavailable: {
    title: 'Service temporarily unavailable',
    description:
      'The service returned a server error or an unavailable status. Check the original response and service health to determine the cause.',
  },
  server_error: {
    title: 'Service internal error',
    description:
      'The endpoint returned a server error status. The status code alone cannot determine whether the gateway or upstream failed; check the original error and request ID.',
  },
  invalid_response: {
    title: 'Empty or unparseable response',
    description:
      'The response is empty, invalid or incompatible with the expected format. Check the original response and channel protocol settings.',
  },
  payment_required: {
    title: 'Payment or subscription required',
    description:
      'The service requires payment or a subscription. Check the original billing requirement and the account involved; HTTP 402 alone does not establish insufficient balance.',
  },
  not_found: {
    title: 'Requested resource not found',
    description:
      'The requested path or resource was not found. Check the endpoint address, resource ID and model name against the original response.',
  },
  channel_configuration: {
    title: 'Channel configuration error',
    description:
      'The gateway could not use the channel configuration. An administrator should check keys, model mappings, headers and the original error.',
  },
  billing_configuration: {
    title: 'Model billing configuration error',
    description:
      'Model billing configuration is missing or invalid. An administrator should check the model price and billing rules against the original error.',
  },
  request_conversion: {
    title: 'Request conversion or encoding failed',
    description:
      'The gateway could not convert or encode the request. Check request data, file formats and channel protocol compatibility against the original error.',
  },
  internal_error: {
    title: 'Gateway internal processing failed',
    description:
      'The gateway encountered an internal processing error. Use the request ID and original error to check server logs.',
  },
  request_cancelled: {
    title: 'Request cancelled',
    description:
      'Request processing was cancelled. Check the original task or acceptance status before deciding whether to submit again.',
  },
  response_read_failed: {
    title: 'Response read failed',
    description:
      'The gateway could not read the complete response. Check the original error and task acceptance status before deciding whether to submit again.',
  },
  token_count_failed: {
    title: 'Request token calculation failed',
    description:
      'The gateway could not calculate request tokens. Check input content, model tokenization settings and the original error.',
  },
  unknown: {
    title: 'Specific cause not identified',
    description:
      'This record does not contain enough evidence to determine the specific cause. The original response is preserved; use the request ID to check server and provider logs.',
  },
} satisfies Record<ErrorReason, { title: string; description: string }>

export function getErrorDiagnosticCopy(
  reason: ErrorReason,
  t: TFunction
): { title: string; description: string } {
  const keys = errorDiagnosticKeys[reason]
  return { title: t(keys.title), description: t(keys.description) }
}
