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
export const relayThemes = [
  {
    id: 'obsidian',
    nameKey: 'Obsidian studio',
    descriptionKey: 'A dark stage. One clear signal.',
    heroKey: 'One API. Your entire workflow.',
  },
  {
    id: 'signal',
    nameKey: 'Signal blue',
    descriptionKey: 'Clear blue. A direct connection.',
    heroKey: 'One account. Your AI workflow.',
  },
  {
    id: 'punch',
    nameKey: 'Orange punch',
    descriptionKey: 'Bold type. A brighter start.',
    heroKey: 'Connect models to your workflow.',
  },
  {
    id: 'circuit',
    nameKey: 'Lime circuit',
    descriptionKey: 'Your next connection, in focus.',
    heroKey: 'Your tools. One connection.',
  },
  {
    id: 'tide',
    nameKey: 'Mint tide',
    descriptionKey: 'A little space to build something new.',
    heroKey: 'Bring your ideas into the flow.',
  },
  {
    id: 'folio',
    nameKey: 'Solar folio',
    descriptionKey: 'Open an account. Open the possibilities.',
    heroKey: 'Create. Connect. Start building.',
  },
] as const

export type RelayTheme = (typeof relayThemes)[number]['id']

export function parseRelayTheme(value: unknown): RelayTheme {
  return relayThemes.find((theme) => theme.id === value)?.id ?? 'obsidian'
}
