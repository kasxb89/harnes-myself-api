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
import { useEffect, useRef, useState } from 'react'

export function useProfitFullscreen() {
  const ref = useRef<HTMLDivElement>(null)
  const [active, setActive] = useState(false)
  const [supported, setSupported] = useState(false)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    const sync = () =>
      setActive(
        ref.current !== null && document.fullscreenElement === ref.current
      )
    setSupported(
      document.fullscreenEnabled === true &&
        typeof ref.current?.requestFullscreen === 'function'
    )
    document.addEventListener('fullscreenchange', sync)
    return () => document.removeEventListener('fullscreenchange', sync)
  }, [])

  const toggle = async () => {
    setFailed(false)
    try {
      if (document.fullscreenElement === ref.current) {
        await document.exitFullscreen()
      } else {
        await ref.current?.requestFullscreen()
      }
    } catch {
      setFailed(true)
    }
  }

  return { ref, active, supported, failed, toggle }
}
