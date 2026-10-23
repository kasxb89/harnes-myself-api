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
import {
  Activity,
  AlertTriangle,
  Box,
  ClipboardList,
  CreditCard,
  FileText,
  FlaskConical,
  Key,
  LayoutDashboard,
  ListTodo,
  MessageSquare,
  PiggyBank,
  PlugZap,
  Radio,
  Receipt,
  ServerCog,
  Settings,
  Ticket,
  Users,
  Wallet,
  Image,
  Video,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import type { SidebarData } from '@/components/layout/types'
import { ROLE } from '@/lib/roles'

export function useSidebarData(): SidebarData {
  const { t } = useTranslation()
  return {
    navGroups: [
      {
        id: 'general',
        title: t('Daily work'),
        items: [
          {
            title: t('Workbench'),
            icon: LayoutDashboard,
            items: [
              {
                title: t('Overview'),
                url: '/dashboard/overview',
                icon: Activity,
              },
              {
                title: t('Model Call Analytics'),
                url: '/dashboard/models',
                icon: LayoutDashboard,
              },
              { title: t('Flow'), url: '/dashboard/flow', icon: Activity },
              {
                title: t('User Analytics'),
                url: '/dashboard/users',
                icon: Users,
                requiredRole: ROLE.ADMIN,
              },
            ],
          },
          {
            title: t('My usage'),
            icon: Key,
            items: [{ title: t('API Keys'), url: '/keys', icon: Key }],
          },
          {
            title: t('Chat sessions'),
            icon: MessageSquare,
            type: 'chat-presets',
            links: [
              {
                title: t('Playground'),
                url: '/playground',
                icon: FlaskConical,
              },
            ],
          },
        ],
      },
      {
        id: 'operations',
        title: t('Platform operations'),
        items: [
          {
            title: t('Operations screen'),
            url: '/operations-screen',
            icon: LayoutDashboard,
            requiredRole: ROLE.ADMIN,
          },
          {
            title: t('Call logs'),
            url: '/usage-logs/common',
            icon: FileText,
          },
          {
            title: t('Image management'),
            url: '/image-management',
            activeUrls: ['/media-operations'],
            icon: Image,
            requiredRole: ROLE.ADMIN,
          },
          {
            title: t('Video management'),
            url: '/video-management',
            activeUrls: ['/media-management', '/video-usage'],
            icon: Video,
            requiredRole: ROLE.ADMIN,
          },
          {
            title: t('Users & assets'),
            icon: Users,
            requiredRole: ROLE.ADMIN,
            items: [
              {
                title: t('Users'),
                url: '/users',
                icon: Users,
                requiredRole: ROLE.ADMIN,
              },
              {
                title: t('User Activity'),
                url: '/user-activity',
                icon: Activity,
                requiredRole: ROLE.ADMIN,
              },
              {
                title: t('Redemption Codes'),
                url: '/redemption-codes',
                icon: Ticket,
                requiredRole: ROLE.ADMIN,
              },
              {
                title: t('Subscriptions'),
                url: '/subscriptions',
                icon: CreditCard,
                requiredRole: ROLE.ADMIN,
              },
            ],
          },
          {
            title: t('Business & finance'),
            icon: PiggyBank,
            requiredRole: ROLE.ADMIN,
            items: [
              {
                title: t('Operations dashboard'),
                url: '/usage-logs/operations',
                configUrls: ['/usage-logs/operations', '/upstream-topups'],
                activeUrls: ['/upstream-topups'],
                icon: Receipt,
                requiredRole: ROLE.ADMIN,
              },
              {
                title: t('Model profits'),
                url: '/model-profits',
                icon: PiggyBank,
                requiredRole: ROLE.ADMIN,
              },
              {
                title: t('Balance ledger'),
                url: '/balance-ledger',
                icon: Wallet,
                requiredRole: ROLE.ADMIN,
              },
            ],
          },
          {
            title: t('Tasks & diagnostics'),
            icon: ClipboardList,
            items: [
              {
                title: t('API Error Logs'),
                url: '/usage-logs/errors',
                icon: AlertTriangle,
                requiredRole: ROLE.ADMIN,
              },
              {
                title: t('Task Logs'),
                url: '/usage-logs/task',
                activeUrls: ['/usage-logs/drawing'],
                configUrls: ['/usage-logs/drawing', '/usage-logs/task'],
                icon: ListTodo,
              },
              {
                title: t('Audit Logs'),
                url: '/usage-logs/audit',
                icon: ClipboardList,
              },
            ],
          },
        ],
      },
      {
        id: 'configuration',
        title: t('Platform configuration'),
        items: [
          {
            title: t('Models & channels'),
            icon: Radio,
            requiredRole: ROLE.ADMIN,
            items: [
              {
                title: t('Channels'),
                url: '/channels',
                icon: Radio,
                requiredRole: ROLE.ADMIN,
              },
              {
                title: t('Model catalog'),
                url: '/models/metadata',
                activeUrls: ['/models/deployments'],
                icon: Box,
                requiredRole: ROLE.ADMIN,
              },
            ],
          },
          {
            title: t('Content & clients'),
            icon: Box,
            requiredRole: ROLE.ADMIN,
            items: [
              {
                title: t('Client model configuration'),
                url: '/client-model-config',
                icon: Box,
                requiredRole: ROLE.ADMIN,
              },
              {
                title: t('AI Creation Management'),
                url: '/creation-content',
                icon: LayoutDashboard,
                requiredRole: ROLE.ADMIN,
              },
              {
                title: t('Announcements'),
                url: '/system-settings/content/announcements',
                icon: FileText,
                requiredRole: ROLE.ADMIN,
              },
              {
                title: t('Customer service QR code'),
                url: '/system-settings/content/customer-service',
                icon: MessageSquare,
                requiredRole: ROLE.ADMIN,
              },
            ],
          },
          {
            title: t('System management'),
            icon: Settings,
            items: [
              {
                title: t('System Settings'),
                url: '/system-settings/site',
                activeUrls: ['/system-settings'],
                icon: Settings,
                requiredRole: ROLE.SUPER_ADMIN,
              },
              {
                title: t('System Info'),
                url: '/system-info',
                icon: ServerCog,
                requiredRole: ROLE.SUPER_ADMIN,
              },
              {
                title: t('Task Plugins'),
                url: '/task-plugins',
                icon: PlugZap,
                requiredRole: ROLE.ADMIN,
              },
            ],
          },
        ],
      },
    ],
  }
}
