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
import robotPortrait from '@/assets/operations/robot-background-v1.png'
import { useMediaQuery } from '@/hooks/use-media-query'

import './robot-background.css'

/** The portrait and eyelids share the image's original 3:2 coordinate space. */
export function RobotBackground(props: { paused: boolean }) {
  const reducedMotion = useMediaQuery('(prefers-reduced-motion: reduce)')

  return (
    <div
      className='ops-robot-background'
      data-paused={props.paused || reducedMotion}
      aria-hidden='true'
    >
      <div className='ops-robot-portrait'>
        <img
          className='ops-robot-image'
          src={robotPortrait}
          alt=''
          draggable={false}
          decoding='async'
        />
        <span className='ops-robot-eye ops-robot-eye--left' />
        <span className='ops-robot-eye ops-robot-eye--right' />
      </div>
    </div>
  )
}
