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
export type SidebarSectionConfig = { enabled: boolean; [key: string]: boolean }
export type SidebarModulesConfig = Record<string, SidebarSectionConfig>

type SidebarModule = { key: string; titleKey: string; descriptionKey: string }
type SidebarModuleSection = {
  key: string
  titleKey: string
  descriptionKey: string
  modules: SidebarModule[]
}

// Navigation, platform defaults and personal preferences share these stable keys.
export const SIDEBAR_MODULE_SECTIONS: SidebarModuleSection[] = [
  {
    key: 'console',
    titleKey: 'Daily work',
    descriptionKey: 'Dashboards, API keys and your request history.',
    modules: [
      {
        key: 'detail',
        titleKey: 'Workbench',
        descriptionKey: 'Aggregated usage metrics and trend charts.',
      },
      {
        key: 'token',
        titleKey: 'API Keys',
        descriptionKey: 'Create, revoke, and audit API tokens.',
      },
      {
        key: 'log',
        titleKey: 'Usage Logs',
        descriptionKey: 'Detailed request logs for investigations.',
      },
      {
        key: 'audit',
        titleKey: 'Audit Logs',
        descriptionKey: 'Login, security and access records',
      },
      {
        key: 'midjourney',
        titleKey: 'Drawing logs',
        descriptionKey: 'History of MjProxy-style image tasks.',
      },
      {
        key: 'task',
        titleKey: 'Task Logs',
        descriptionKey: 'Background job tracker for queued work.',
      },
      {
        key: 'errors',
        titleKey: 'API Error Logs',
        descriptionKey: 'Investigate failed upstream requests.',
      },
    ],
  },
  {
    key: 'chat',
    titleKey: 'Chat sessions',
    descriptionKey: 'Playground experiments and live conversations.',
    modules: [
      {
        key: 'playground',
        titleKey: 'Playground',
        descriptionKey: 'Experiment with prompts and models in real time.',
      },
      {
        key: 'chat',
        titleKey: 'Chat',
        descriptionKey: 'Access previous conversations and start new ones.',
      },
    ],
  },
  {
    key: 'finance',
    titleKey: 'Business & finance',
    descriptionKey: 'Revenue, model costs and balance changes.',
    modules: [
      {
        key: 'operations',
        titleKey: 'Operations dashboard',
        descriptionKey: 'Review platform revenue and recharges.',
      },
      {
        key: 'model_profits',
        titleKey: 'Model profits',
        descriptionKey: 'Compare model revenue and configured costs.',
      },
      {
        key: 'balance_ledger',
        titleKey: 'Balance ledger',
        descriptionKey: 'Review grants, top-ups and administrator adjustments.',
      },
      {
        key: 'upstream_topups',
        titleKey: 'Upstream top-ups',
        descriptionKey: 'Manage upstream account funding records.',
      },
    ],
  },
  {
    key: 'users',
    titleKey: 'Users & assets',
    descriptionKey: 'Accounts, subscriptions and redemption codes.',
    modules: [
      {
        key: 'user',
        titleKey: 'Users',
        descriptionKey: 'Administer user accounts and roles.',
      },
      {
        key: 'user_activity',
        titleKey: 'User Activity',
        descriptionKey: 'Review account activity and usage.',
      },
      {
        key: 'redemption',
        titleKey: 'Redemption Codes',
        descriptionKey: 'Create and review invite or credit codes.',
      },
      {
        key: 'subscription',
        titleKey: 'Subscriptions',
        descriptionKey: 'Manage subscription plans and pricing.',
      },
    ],
  },
  {
    key: 'models',
    titleKey: 'Models & channels',
    descriptionKey: 'Upstream routing and the model catalog.',
    modules: [
      {
        key: 'channel',
        titleKey: 'Channels',
        descriptionKey: 'Configure upstream providers and routing.',
      },
      {
        key: 'models',
        titleKey: 'Model catalog',
        descriptionKey: 'Manage catalog visibility and pricing.',
      },
    ],
  },
  {
    key: 'content',
    titleKey: 'Content & clients',
    descriptionKey: 'Client models, creative content and customer support.',
    modules: [
      {
        key: 'client_model_config',
        titleKey: 'Client model configuration',
        descriptionKey: 'Manage client model names, order and descriptions.',
      },
      {
        key: 'creation_content',
        titleKey: 'AI Creation Management',
        descriptionKey: 'Manage creative templates and published content.',
      },
      {
        key: 'announcements',
        titleKey: 'Announcements',
        descriptionKey: 'Publish platform notices for clients.',
      },
      {
        key: 'customer_service',
        titleKey: 'Customer service QR code',
        descriptionKey: 'Upload and save the customer service QR code.',
      },
    ],
  },
  {
    key: 'media',
    titleKey: 'Tasks & diagnostics',
    descriptionKey: 'Media tasks, concurrency and usage analytics.',
    modules: [
      {
        key: 'operations',
        titleKey: 'Image and video operations',
        descriptionKey: 'Inspect media tasks and upstream concurrency.',
      },
      {
        key: 'video_usage',
        titleKey: 'Video model usage',
        descriptionKey: 'Review video generation usage.',
      },
    ],
  },
  {
    key: 'system',
    titleKey: 'System management',
    descriptionKey: 'Platform settings, runtime information and plugins.',
    modules: [
      {
        key: 'setting',
        titleKey: 'System Settings',
        descriptionKey: 'Advanced platform configuration.',
      },
      {
        key: 'info',
        titleKey: 'System Info',
        descriptionKey: 'View server runtime information.',
      },
      {
        key: 'task_plugin',
        titleKey: 'Task Plugins',
        descriptionKey: 'Manage task protocol plugins.',
      },
    ],
  },
  {
    key: 'personal',
    titleKey: 'Account menu',
    descriptionKey: 'Wallet, profile and security entries in the account menu.',
    modules: [
      {
        key: 'topup',
        titleKey: 'Wallet',
        descriptionKey: 'Top up balance and view billing history.',
      },
      {
        key: 'personal',
        titleKey: 'Profile',
        descriptionKey: 'Personal settings and profile management.',
      },
      {
        key: 'security',
        titleKey: 'Security & Access',
        descriptionKey: 'Manage your security settings and account access',
      },
    ],
  },
]

export const SIDEBAR_MODULES_DEFAULT: SidebarModulesConfig = Object.fromEntries(
  SIDEBAR_MODULE_SECTIONS.map((section) => [
    section.key,
    {
      enabled: true,
      ...Object.fromEntries(
        section.modules.map((module) => [module.key, true])
      ),
    },
  ])
)

const LEGACY_ADMIN_MODULES: Record<string, Record<string, string>> = {
  users: {
    user: 'user',
    redemption: 'redemption',
    subscription: 'subscription',
    user_activity: 'user',
  },
  models: { channel: 'channel', models: 'models' },
  system: { setting: 'setting' },
}
const ADMIN_BUSINESS_SECTIONS = new Set([
  'finance',
  'users',
  'models',
  'content',
  'media',
  'system',
])

function asBoolean(value: unknown, fallback: boolean): boolean {
  if (typeof value === 'boolean') return value
  if (typeof value === 'number') return value === 1
  if (typeof value === 'string') {
    const normalized = value.trim().toLowerCase()
    if (normalized === 'true' || normalized === '1') return true
    if (normalized === 'false' || normalized === '0') return false
  }
  return fallback
}

export function parseSidebarModules(
  value: string | null | undefined
): SidebarModulesConfig {
  let parsed: Record<string, unknown> = {}
  try {
    const candidate: unknown = value ? JSON.parse(value) : {}
    if (
      candidate &&
      typeof candidate === 'object' &&
      !Array.isArray(candidate)
    ) {
      parsed = candidate as Record<string, unknown>
    }
  } catch {
    /* Empty or malformed historical values use the platform defaults. */
  }
  const result: SidebarModulesConfig = Object.create(null)
  for (const [key, raw] of Object.entries(parsed)) {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) continue
    result[key] = Object.fromEntries(
      Object.entries(raw).map(([module, enabled]) => [
        module,
        asBoolean(enabled, true),
      ])
    ) as SidebarSectionConfig
    result[key].enabled ??= true
  }
  const legacyAdmin = result.admin
  for (const [section, defaults] of Object.entries(SIDEBAR_MODULES_DEFAULT)) {
    const saved = result[section]
    const migrated: SidebarSectionConfig = { ...defaults }
    if (
      section === 'console' &&
      saved?.errors === undefined &&
      saved?.log !== undefined
    ) {
      migrated.errors = saved.log
    }
    if (legacyAdmin && ADMIN_BUSINESS_SECTIONS.has(section)) {
      migrated.enabled = legacyAdmin.enabled !== false
      for (const [module, legacyModule] of Object.entries(
        LEGACY_ADMIN_MODULES[section] ?? {}
      )) {
        if (legacyAdmin[legacyModule] !== undefined) {
          migrated[module] = legacyAdmin[legacyModule]
        }
      }
    }
    result[section] = { ...migrated, ...saved }
  }
  return result
}

export const SIDEBAR_URL_MODULES: Record<
  string,
  { section: string; module: string }
> = {
  '/playground': { section: 'chat', module: 'playground' },
  '/dashboard': { section: 'console', module: 'detail' },
  '/dashboard/overview': { section: 'console', module: 'detail' },
  '/dashboard/models': { section: 'console', module: 'detail' },
  '/dashboard/flow': { section: 'console', module: 'detail' },
  '/dashboard/users': { section: 'console', module: 'detail' },
  '/keys': { section: 'console', module: 'token' },
  '/usage-logs': { section: 'console', module: 'log' },
  '/usage-logs/common': { section: 'console', module: 'log' },
  '/usage-logs/errors': { section: 'console', module: 'errors' },
  '/usage-logs/audit': { section: 'console', module: 'audit' },
  '/usage-logs/drawing': { section: 'console', module: 'midjourney' },
  '/usage-logs/task': { section: 'console', module: 'task' },
  '/usage-logs/operations': { section: 'finance', module: 'operations' },
  '/model-profits': { section: 'finance', module: 'model_profits' },
  '/balance-ledger': { section: 'finance', module: 'balance_ledger' },
  '/upstream-topups': { section: 'finance', module: 'upstream_topups' },
  '/users': { section: 'users', module: 'user' },
  '/user-activity': { section: 'users', module: 'user_activity' },
  '/redemption-codes': { section: 'users', module: 'redemption' },
  '/subscriptions': { section: 'users', module: 'subscription' },
  '/channels': { section: 'models', module: 'channel' },
  '/models': { section: 'models', module: 'models' },
  '/models/metadata': { section: 'models', module: 'models' },
  '/models/deployments': { section: 'models', module: 'models' },
  '/client-model-config': { section: 'content', module: 'client_model_config' },
  '/creation-content': { section: 'content', module: 'creation_content' },
  '/system-settings/content/announcements': {
    section: 'content',
    module: 'announcements',
  },
  '/system-settings/content/customer-service': {
    section: 'content',
    module: 'customer_service',
  },
  '/media-operations': { section: 'media', module: 'operations' },
  '/image-management': { section: 'media', module: 'operations' },
  '/video-management': { section: 'media', module: 'video_usage' },
  '/video-usage': { section: 'media', module: 'video_usage' },
  '/system-info': { section: 'system', module: 'info' },
  '/task-plugins': { section: 'system', module: 'task_plugin' },
  '/system-settings': { section: 'system', module: 'setting' },
  '/system-settings/site': { section: 'system', module: 'setting' },
  '/wallet': { section: 'personal', module: 'topup' },
  '/profile': { section: 'personal', module: 'personal' },
  '/security': { section: 'personal', module: 'security' },
}
