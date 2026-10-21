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
import { describe, expect, test } from 'vitest'

import { parseErrorDiagnostics } from '../error-diagnostics'

const upstreamError = {
  error_code: 'bad_response_status_code',
  error_type: 'openai_error',
  status_code: 402,
}

const upstreamContent = (type: string, message: string, code?: string) =>
  JSON.stringify({ error: { type, message, code } })

describe('parseErrorDiagnostics', () => {
  test('extracts the H3 upstream balance reason from the original logged response', () => {
    const content =
      'status_code=402, {"type":"error","error":{"type":"insufficient_balance_error","message":"H3 积分余额不足 (1008)","http_code":"402"},"request_id":"630b6b51-330e-4797-8071-1842039490aa"}'

    expect(parseErrorDiagnostics(content, upstreamError)).toEqual({
      message: 'H3 积分余额不足 (1008)',
      type: 'insufficient_balance_error',
      reason: 'upstream_balance',
      source: 'upstream',
      upstream: true,
      balanceInsufficient: true,
    })
  })

  test.each([
    { status: 402, reason: 'payment_required' },
    { status: 403, reason: 'permission' },
    { status: 429, reason: 'rate_limit' },
  ])(
    'uses HTTP $status only for a broad diagnosis without inventing an upstream balance cause',
    ({ status, reason }) => {
      expect(
        parseErrorDiagnostics(`status_code=${status}, Request rejected`, {
          ...upstreamError,
          status_code: status,
        })
      ).toMatchObject({
        message: 'Request rejected',
        reason,
        source: 'unknown',
        upstream: false,
        balanceInsufficient: false,
      })
    }
  )

  test('does not attribute a structured balance error to upstream without trusted log metadata', () => {
    expect(
      parseErrorDiagnostics(
        '{"error":{"code":"insufficient_balance","message":"Balance unavailable"}}',
        null
      )
    ).toMatchObject({
      reason: 'unknown',
      source: 'unknown',
      balanceInsufficient: false,
    })
  })

  test.each([
    ['insufficient_balance_error', 'upstream_balance'],
    ['authentication_error', 'authentication'],
    ['permission_error', 'permission'],
    ['rate_limit_error', 'rate_limit'],
    ['concurrency_limit_exceeded', 'concurrency_limit'],
    ['model_not_found', 'model_unavailable'],
    ['invalid_request_error', 'invalid_request'],
    ['context_length_exceeded', 'context_length'],
    ['content_policy_violation', 'content_policy'],
    ['request_too_large', 'payload_too_large'],
    ['unsupported_operation', 'unsupported_operation'],
    ['overloaded_error', 'upstream_unavailable'],
    ['payment_required', 'payment_required'],
    ['not_found', 'not_found'],
  ])(
    'classifies explicit structured provider code %s as %s',
    (code, reason) => {
      expect(
        parseErrorDiagnostics(
          upstreamContent('api_error', 'Provider rejected the request', code),
          upstreamError
        )
      ).toMatchObject({ reason, source: 'upstream', code })
    }
  )

  test('prefers a specific context error code over a generic invalid-request type and HTTP status', () => {
    expect(
      parseErrorDiagnostics(
        upstreamContent(
          'invalid_request_error',
          'Request rejected',
          'context_length_exceeded'
        ),
        { ...upstreamError, status_code: 400 }
      )
    ).toMatchObject({ reason: 'context_length', source: 'upstream' })
  })

  test('does not let untrusted message words override a specific structured permission error', () => {
    expect(
      parseErrorDiagnostics(
        upstreamContent(
          'permission_error',
          'Your credit balance is too low to access this API; insufficient_user_quota'
        ),
        upstreamError
      )
    ).toMatchObject({ reason: 'permission', source: 'upstream' })
  })

  test.each([
    [
      'Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits.',
      'upstream_balance',
    ],
    ['prompt is too long: 212789 tokens > 200000 maximum', 'context_length'],
  ])(
    'refines a generic provider invalid_request_error using its explicit cause: %s',
    (message, reason) => {
      expect(
        parseErrorDiagnostics(
          upstreamContent('invalid_request_error', message),
          {
            error_type: 'claude_error',
            error_code: 'bad_response_status_code',
            status_code: 400,
          }
        )
      ).toMatchObject({ message, reason, source: 'upstream' })
    }
  )

  test.each([
    {
      content:
        'status_code=429, You exceeded your current quota, please check your plan and billing details.',
      other: {
        error_type: 'openai_error',
        error_code: 'insufficient_quota',
        status_code: 429,
      },
      reason: 'upstream_balance',
    },
    {
      content:
        'status_code=400, Your credit balance is too low to access the Anthropic API.',
      other: {
        error_type: 'claude_error',
        error_code: 'invalid_request_error',
        status_code: 400,
      },
      reason: 'upstream_balance',
    },
    {
      content:
        'status_code=400, prompt is too long: 212789 tokens > 200000 maximum',
      other: {
        error_type: 'claude_error',
        error_code: 'invalid_request_error',
        status_code: 400,
      },
      reason: 'context_length',
    },
    {
      content:
        'status_code=403, Your credit balance is too low to access this API.',
      other: {
        error_type: 'openai_error',
        error_code: 'permission_denied',
        status_code: 403,
      },
      reason: 'permission',
    },
  ])(
    'uses provider metadata when the standard relay only logged the plain message: $other.error_code',
    ({ content, other, reason }) => {
      expect(parseErrorDiagnostics(content, other)).toMatchObject({
        reason,
        source: 'upstream',
      })
    }
  )

  test.each([
    ['access_denied', 'openai_error', 'permission', 'unknown'],
    ['access_denied', 'new_api_error', 'permission', 'gateway'],
    ['invalid_request', 'openai_error', 'invalid_request', 'unknown'],
    ['invalid_request', 'new_api_error', 'invalid_request', 'gateway'],
  ])(
    'keeps shared code %s with type %s factual as %s and source %s',
    (code, type, reason, source) => {
      expect(
        parseErrorDiagnostics('status_code=403, Provider access denied', {
          error_type: type,
          error_code: code,
          status_code: 403,
        })
      ).toMatchObject({ reason, source, upstream: false })
    }
  )

  test.each(['openai_error', 'claude_error'])(
    'does not attribute an ambiguous insufficient_user_quota code from %s to a user or upstream balance',
    (type) => {
      expect(
        parseErrorDiagnostics(
          upstreamContent(
            'insufficient_balance_error',
            'Account balance is low'
          ),
          {
            error_type: type,
            error_code: 'insufficient_user_quota',
            status_code: 403,
          }
        )
      ).toMatchObject({
        reason: 'unknown',
        source: 'unknown',
        upstream: false,
        balanceInsufficient: false,
      })
    }
  )

  test('keeps a provider-shaped token pre-consumption error factual without claiming its origin or an insufficient balance', () => {
    expect(
      parseErrorDiagnostics('Token quota pre-consumption failed', {
        error_type: 'openai_error',
        error_code: 'pre_consume_token_quota_failed',
        status_code: 403,
      })
    ).toMatchObject({
      reason: 'token_quota',
      source: 'unknown',
      upstream: false,
      balanceInsufficient: false,
    })
  })

  test.each([
    ['insufficient_user_quota', 'user_balance'],
    ['pre_consume_token_quota_failed', 'token_quota'],
    ['bad_request_body', 'invalid_request'],
    ['sensitive_words_detected', 'content_policy'],
    ['channel:no_available_key', 'no_available_channel'],
    ['get_channel_failed', 'no_available_channel'],
    ['channel:invalid_key', 'channel_configuration'],
    ['channel:param_override_invalid', 'channel_configuration'],
    ['invalid_api_type', 'channel_configuration'],
    ['model_price_error', 'billing_configuration'],
    ['count_token_failed', 'token_count_failed'],
    ['json_marshal_failed', 'request_conversion'],
    ['convert_request_failed', 'request_conversion'],
    ['gen_relay_info_failed', 'internal_error'],
    ['read_response_body_failed', 'response_read_failed'],
    ['bad_response_body', 'invalid_response'],
    ['channel:response_time_exceeded', 'timeout'],
  ])(
    'keeps trusted local code %s classified as %s despite conflicting provider text',
    (code, reason) => {
      expect(
        parseErrorDiagnostics(
          upstreamContent(
            'insufficient_balance_error',
            'Upstream credits exhausted'
          ),
          { error_code: code, error_type: 'new_api_error', status_code: 403 }
        )
      ).toMatchObject({
        reason,
        source: 'gateway',
        upstream: false,
        balanceInsufficient: false,
      })
    }
  )

  test('distinguishes a local model-without-channel error from a provider missing-model error', () => {
    expect(
      parseErrorDiagnostics('No channel for requested model', {
        error_code: 'model_not_found',
        error_type: 'new_api_error',
        status_code: 503,
      })
    ).toMatchObject({ reason: 'no_available_channel', source: 'gateway' })
  })

  test('does not trust a payload local code as evidence that the gateway rejected a request', () => {
    expect(
      parseErrorDiagnostics(
        '{"error":{"code":"insufficient_user_quota","message":"insufficient_user_quota"}}',
        upstreamError
      )
    ).toMatchObject({
      reason: 'payment_required',
      source: 'unknown',
      upstream: false,
    })
  })

  test.each([
    [
      'Post "https://provider.invalid": context deadline exceeded (Client.Timeout exceeded while awaiting headers)',
      'timeout',
    ],
    ['dial tcp: lookup provider.invalid: no such host', 'dns'],
    ['dial tcp 192.0.2.1:443: connect: connection refused', 'connection'],
    ['read tcp 192.0.2.1:443: connection reset by peer', 'connection'],
    ['remote error: tls: handshake failure', 'tls'],
    [
      'tls: failed to verify certificate: x509: certificate signed by unknown authority',
      'tls',
    ],
    ['Post "https://provider.invalid": context canceled', 'request_cancelled'],
    ['Post "https://provider.invalid": EOF', 'connection'],
  ])('explains Go transport failure %s as %s', (message, reason) => {
    expect(
      parseErrorDiagnostics(message, {
        error_code: 'do_request_failed',
        error_type: 'new_api_error',
        status_code: 500,
      })
    ).toMatchObject({ message, reason, source: 'gateway' })
  })

  test('recognizes a plain timeout message without requiring JSON or an upstream claim', () => {
    expect(parseErrorDiagnostics('i/o timeout', null)).toMatchObject({
      message: 'i/o timeout',
      reason: 'timeout',
      source: 'unknown',
    })
  })

  test.each([
    {
      content:
        '{"error":"Video creation is unavailable","code":"provider_down"}',
      expected: {
        message: 'Video creation is unavailable',
        code: 'provider_down',
      },
    },
    {
      content: '{"message":"Permission denied","type":"permission_error"}',
      expected: { message: 'Permission denied', type: 'permission_error' },
    },
    {
      content: '{"detail":"The input image is invalid"}',
      expected: { message: 'The input image is invalid' },
    },
    {
      content:
        '{"error":{"message":"Keep <b>literal</b> text and whitespace  ","code":1008,"type":null}}',
      expected: {
        message: 'Keep <b>literal</b> text and whitespace  ',
        code: '1008',
      },
    },
  ])(
    'extracts a supported JSON envelope while preserving literal text: $content',
    ({ content, expected }) => {
      expect(parseErrorDiagnostics(content, upstreamError)).toMatchObject(
        expected
      )
    }
  )

  test('reads the MiniMax base_resp message and numeric code without guessing from a numeric code alone', () => {
    expect(
      parseErrorDiagnostics(
        '{"base_resp":{"status_code":1008,"status_msg":"Unknown provider failure"}}',
        upstreamError
      )
    ).toMatchObject({
      message: 'Unknown provider failure',
      code: '1008',
      reason: 'payment_required',
      source: 'upstream',
    })
  })

  test.each([
    '<!DOCTYPE html><html><body>502 Bad Gateway</body></html>',
    'status_code=502, {"error":',
    'status_code=502, ',
  ])(
    'explains an HTML, malformed JSON, or missing response body as an invalid response: %s',
    (content) => {
      expect(
        parseErrorDiagnostics(content, { ...upstreamError, status_code: 502 })
      ).toMatchObject({ reason: 'invalid_response' })
    }
  )

  test.each([
    'Unexpected task failure',
    'H3 积分余额不足 (1008)',
    'prefix {"error":{"message":"Do not parse unknown prefixes"}}',
    '{"response":{"error":{"message":"Do not traverse arbitrary structures"}}}',
    '{"error":{"message":{"text":"Do not traverse arbitrary structures"}}}',
    '[{"message":"Do not traverse arrays"}]',
    'null',
  ])(
    'preserves unknown content without inventing a root cause: %s',
    (content) => {
      expect(parseErrorDiagnostics(content, null)).toMatchObject({
        message: content,
        reason: 'unknown',
        source: 'unknown',
        upstream: false,
        balanceInsufficient: false,
      })
    }
  )

  test('explains an unknown local HTTP 500 neutrally without claiming an upstream outage', () => {
    expect(
      parseErrorDiagnostics('Unexpected failure', {
        error_code: 'unexpected_local_failure',
        error_type: 'new_api_error',
        status_code: 500,
      })
    ).toMatchObject({ reason: 'server_error', source: 'unknown' })
  })

  test('does not classify a cause hidden inside an unsupported JSON structure', () => {
    const content =
      '{"response":{"error":{"message":"Permission denied: no such host"}}}'
    expect(parseErrorDiagnostics(content, null)).toMatchObject({
      message: content,
      reason: 'unknown',
      source: 'unknown',
    })
  })

  test('does not treat a generic api_error type as proof of an upstream outage', () => {
    expect(
      parseErrorDiagnostics(upstreamContent('api_error', 'Request rejected'), {
        ...upstreamError,
        status_code: 500,
      })
    ).toMatchObject({ reason: 'server_error', source: 'unknown' })
  })

  test.each([
    [500, 'server_error'],
    [502, 'server_error'],
    [503, 'server_error'],
    [415, 'invalid_request'],
    [520, 'unknown'],
  ])(
    'explains HTTP %s without inventing a provider-specific root cause',
    (status, reason) => {
      expect(
        parseErrorDiagnostics('Request rejected', {
          status_code: Number(status),
        })
      ).toMatchObject({ reason, source: 'unknown' })
    }
  )

  test.each([
    ['not_found', 404, 'not_found', 'upstream'],
    ['auth', 401, 'authentication', 'upstream'],
    ['auth', 403, 'permission', 'upstream'],
    ['transient', 429, 'rate_limit', 'upstream'],
    ['transient', 503, 'upstream_unavailable', 'upstream'],
    ['unrecognized', 200, 'invalid_response', 'unknown'],
    ['hook_error', 200, 'invalid_response', 'gateway'],
    ['transport_error', 0, 'connection', 'gateway'],
    ['task_timeout', 504, 'timeout', 'gateway'],
  ])(
    'classifies durable task error %s with status %s as %s',
    (code, status, reason, source) => {
      expect(
        parseErrorDiagnostics('Failed to query the upstream task status', {
          error_type: 'task_error',
          error_code: String(code),
          status_code: Number(status),
        })
      ).toMatchObject({ reason, source })
    }
  )

  test('does not turn a generic task terminal failure into an upstream outage just because its stored status is 502', () => {
    expect(
      parseErrorDiagnostics('Generation failed for an unknown reason', {
        error_type: 'task_error',
        error_code: 'task_failed',
        status_code: 502,
      })
    ).toMatchObject({ reason: 'unknown', source: 'unknown' })
  })

  test('does not label a local task error as upstream merely because it has the generic response wrapper', () => {
    expect(
      parseErrorDiagnostics('status_code=400, video_id is required', {
        ...upstreamError,
        status_code: 400,
      })
    ).toMatchObject({
      reason: 'invalid_request',
      source: 'unknown',
      upstream: false,
    })
  })

  test('returns a diagnosis when a stable local code is available without content', () => {
    expect(
      parseErrorDiagnostics('', {
        error_code: 'insufficient_user_quota',
        status_code: 403,
      })
    ).toMatchObject({
      message: '',
      reason: 'user_balance',
      source: 'gateway',
    })
  })

  test('returns null only when neither content nor useful metadata exists', () => {
    expect(parseErrorDiagnostics('   ', null)).toBeNull()
    expect(parseErrorDiagnostics('', { status_code: 0 })).toBeNull()
  })
})
