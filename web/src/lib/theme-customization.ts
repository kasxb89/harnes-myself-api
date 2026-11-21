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
export const THEME_PRESETS = [
  {
    value: 'obsidian',
    name: 'Obsidian studio',
    swatches: ['#000000', '#fc1c46'],
  },
] as const
export type ThemePreset = 'obsidian'
export type ThemeFont = 'default' | 'sans' | 'serif'
export type ResolvedThemeFont = 'system' | 'sans' | 'serif'
export type ThemeRadius = 'default' | 'none' | 'sm' | 'md' | 'lg' | 'xl'
export type ThemeScale = 'default' | 'sm' | 'lg' | 'xl'
export type ContentLayout = 'full' | 'centered'
export type ThemeCustomization = {
  preset: ThemePreset
  font: ThemeFont
  radius: ThemeRadius
  scale: ThemeScale
  contentLayout: ContentLayout
}
export const DEFAULT_THEME_CUSTOMIZATION: ThemeCustomization = {
  preset: 'obsidian',
  font: 'sans',
  radius: 'none',
  scale: 'default',
  contentLayout: 'full',
}
export const THEME_PRESET_VALUES: ReadonlySet<ThemePreset> = new Set([
  'obsidian',
])
export const THEME_FONT_VALUES: ReadonlySet<ThemeFont> = new Set(['sans'])
export const THEME_RADIUS_VALUES: ReadonlySet<ThemeRadius> = new Set(['none'])
export const THEME_SCALE_VALUES: ReadonlySet<ThemeScale> = new Set(['default'])
export const CONTENT_LAYOUT_VALUES: ReadonlySet<ContentLayout> = new Set([
  'full',
])
export const PRESET_DEFAULT_FONT: Partial<
  Record<ThemePreset, ResolvedThemeFont>
> = { obsidian: 'sans' }
export function resolveThemeFont(
  _font: ThemeFont,
  _preset: ThemePreset
): ResolvedThemeFont {
  return 'sans'
}
