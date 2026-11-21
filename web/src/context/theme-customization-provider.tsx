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
import { createContext, useContext, useEffect } from 'react'

import {
  DEFAULT_THEME_CUSTOMIZATION,
  type ContentLayout,
  type ThemeCustomization,
  type ThemeFont,
  type ThemePreset,
  type ThemeRadius,
  type ThemeScale,
} from '@/lib/theme-customization'

type ThemeCustomizationContextType = {
  defaults: ThemeCustomization
  customization: ThemeCustomization
  setPreset: (preset: ThemePreset) => void
  setFont: (font: ThemeFont) => void
  setRadius: (radius: ThemeRadius) => void
  setScale: (scale: ThemeScale) => void
  setContentLayout: (layout: ContentLayout) => void
  resetCustomization: () => void
}
const canonical: ThemeCustomizationContextType = {
  defaults: DEFAULT_THEME_CUSTOMIZATION,
  customization: DEFAULT_THEME_CUSTOMIZATION,
  setPreset: () => {},
  setFont: () => {},
  setRadius: () => {},
  setScale: () => {},
  setContentLayout: () => {},
  resetCustomization: () => {},
}
const ThemeCustomizationContext =
  createContext<ThemeCustomizationContextType>(canonical)
export function ThemeCustomizationProvider(props: {
  children: React.ReactNode
}) {
  useEffect(() => {
    const body = document.body
    body.dataset.themePreset = 'obsidian'
    body.dataset.themeFont = 'sans'
    body.dataset.themeRadius = 'none'
    body.dataset.themeScale = 'default'
    body.dataset.themeContentLayout = 'full'
  }, [])
  return (
    <ThemeCustomizationContext value={canonical}>
      {props.children}
    </ThemeCustomizationContext>
  )
}
// eslint-disable-next-line react-refresh/only-export-components
export function useThemeCustomization() {
  return useContext(ThemeCustomizationContext)
}
