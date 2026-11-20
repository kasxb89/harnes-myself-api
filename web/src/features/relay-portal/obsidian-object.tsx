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
import { useEffect, useId, useRef, useState } from 'react'

import { useMediaQuery } from '@/hooks/use-media-query'

/** An original decorative knot; it never reads account or request data. */
export function ObsidianObject(props: { running: boolean }) {
  const hostRef = useRef<HTMLDivElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const controllerRef = useRef<(() => void) | null>(null)
  const reducedMotion = useMediaQuery('(prefers-reduced-motion: reduce)')
  const pausedRef = useRef(!props.running || reducedMotion)
  const [rendered, setRendered] = useState(false)
  const gradientId = useId()

  useEffect(() => {
    pausedRef.current = !props.running || reducedMotion
    controllerRef.current?.()
  }, [props.running, reducedMotion])

  useEffect(() => {
    const host = hostRef.current
    const canvas = canvasRef.current
    if (!host || !canvas) return
    let mounted = true
    let visible = typeof IntersectionObserver === 'undefined'
    let importing = false
    let failed = false
    let frame = 0
    let phase = 0
    let lastPaint: number | undefined
    let width = 0
    let height = 0
    let scene:
      | {
          render: () => void
          resize: (width: number, height: number) => void
        }
      | undefined
    const resources: { dispose: () => void }[] = []
    const own = <T extends { dispose: () => void }>(resource: T): T => {
      resources.push(resource)
      return resource
    }
    const stop = () => {
      if (frame) cancelAnimationFrame(frame)
      frame = 0
      lastPaint = undefined
    }
    const release = () => {
      scene = undefined
      for (const resource of resources.splice(0).reverse()) {
        try {
          resource.dispose()
        } catch {
          // A lost GPU context must not prevent the remaining owned resources
          // from being released or hide the static illustration.
        }
      }
    }
    const fail = () => {
      failed = true
      stop()
      release()
      if (mounted) setRendered(false)
    }
    const inView = () =>
      visible &&
      document.visibilityState !== 'hidden' &&
      host.clientWidth > 0 &&
      host.clientHeight > 0
    const paint = (timestamp: number) => {
      frame = 0
      if (!scene || pausedRef.current || !inView()) {
        lastPaint = undefined
        return
      }
      if (lastPaint === undefined) lastPaint = timestamp
      if (timestamp - lastPaint >= 1000 / 30) {
        phase += Math.min((timestamp - lastPaint) / 1000, 0.05)
        lastPaint = timestamp
        try {
          scene.render()
        } catch {
          fail()
          return
        }
      }
      frame = requestAnimationFrame(paint)
    }
    const sync = () => {
      if (!mounted || failed || !inView()) {
        stop()
        return
      }
      if (!scene) {
        if (importing) return
        importing = true
        void import('three')
          .then((THREE) => {
            if (!mounted || failed) return
            const renderer = new THREE.WebGLRenderer({
              canvas,
              alpha: true,
              antialias: true,
              powerPreference: 'low-power',
            })
            own({
              dispose: () => {
                renderer.dispose()
                if (!renderer.getContext().isContextLost()) {
                  renderer.forceContextLoss()
                }
              },
            })
            renderer.setClearColor(0x000000, 0)
            renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.5))
            renderer.outputColorSpace = THREE.SRGBColorSpace
            renderer.toneMapping = THREE.ACESFilmicToneMapping
            renderer.toneMappingExposure = 1.35
            const world = new THREE.Scene()
            const camera = new THREE.PerspectiveCamera(36, 1, 0.1, 40)
            camera.position.set(0, 0.1, 7.8)
            camera.lookAt(0, 0, 0)

            // Local HDR light cards give black chrome readable, broad reflections.
            const studio = new THREE.Scene()
            studio.background = new THREE.Color(0x25262a)
            const panel = own(new THREE.PlaneGeometry(1, 1))
            const white = own(
              new THREE.MeshBasicMaterial({
                color: new THREE.Color(0xffffff).multiplyScalar(7),
                side: THREE.DoubleSide,
              })
            )
            for (const [x, y, z, w, h] of [
              [-3.5, 2, 3, 3.5, 5],
              [4, 0.5, 1, 1.2, 5],
              [0, 4, -1, 5, 1.3],
              [-1, -3, 2, 4, 0.8],
              [1, 1, -4, 3, 3],
            ]) {
              const card = new THREE.Mesh(panel, white)
              card.position.set(x, y, z)
              card.scale.set(w, h, 1)
              card.lookAt(0, 0, 0)
              studio.add(card)
            }
            const pmrem = own(new THREE.PMREMGenerator(renderer))
            const environment = own(
              pmrem.fromScene(studio, 0.025, 0.1, 25, { size: 128 })
            )
            world.environment = environment.texture
            studio.clear()
            const geometry = own(
              new THREE.TorusKnotGeometry(1.35, 0.43, 220, 32, 2, 3)
            )
            const metal = own(
              new THREE.MeshPhysicalMaterial({
                color: 0x3c3f45,
                metalness: 1,
                roughness: 0.2,
                clearcoat: 1,
                clearcoatRoughness: 0.12,
                envMapIntensity: 2.3,
              })
            )
            const knot = new THREE.Mesh(geometry, metal)
            world.add(knot)
            const key = new THREE.DirectionalLight(0xffffff, 4)
            key.position.set(-3, 4, 5)
            world.add(key)
            const rim = new THREE.DirectionalLight(0xe4e9f2, 2)
            rim.position.set(4, -2, -3)
            world.add(rim)
            scene = {
              resize: (w, h) => {
                renderer.setSize(w, h, false)
                camera.aspect = w / h
                camera.position.z = 7.8 / Math.min(camera.aspect, 1)
                camera.updateProjectionMatrix()
              },
              render: () => {
                knot.rotation.set(
                  0.4 + Math.sin(phase * 0.12) * 0.1,
                  -0.35 + phase * 0.11,
                  0.2 + Math.sin(phase * 0.08) * 0.06
                )
                renderer.render(world, camera)
              },
            }
            sync()
          })
          .catch(fail)
        return
      }
      const nextWidth = Math.ceil(host.clientWidth)
      const nextHeight = Math.ceil(host.clientHeight)
      if (nextWidth !== width || nextHeight !== height) {
        width = nextWidth
        height = nextHeight
        try {
          scene.resize(width, height)
          scene.render()
          setRendered(true)
        } catch {
          fail()
          return
        }
      }
      if (pausedRef.current) stop()
      else if (!frame) frame = requestAnimationFrame(paint)
    }
    const onContextLost = (event: Event) => {
      event.preventDefault()
      fail()
    }
    const intersection =
      typeof IntersectionObserver === 'undefined'
        ? undefined
        : new IntersectionObserver(([entry]) => {
            visible = entry?.isIntersecting ?? false
            sync()
          })
    const resize =
      typeof ResizeObserver === 'undefined'
        ? undefined
        : new ResizeObserver(sync)
    controllerRef.current = sync
    intersection?.observe(host)
    resize?.observe(host)
    window.addEventListener('resize', sync)
    document.addEventListener('visibilitychange', sync)
    canvas.addEventListener('webglcontextlost', onContextLost)
    sync()
    return () => {
      mounted = false
      controllerRef.current = null
      stop()
      intersection?.disconnect()
      resize?.disconnect()
      window.removeEventListener('resize', sync)
      document.removeEventListener('visibilitychange', sync)
      canvas.removeEventListener('webglcontextlost', onContextLost)
      release()
    }
  }, [])

  return (
    <div
      ref={hostRef}
      className='relay-obsidian-object'
      data-renderer={rendered ? 'webgl' : 'fallback'}
      data-paused={!props.running || reducedMotion}
      aria-hidden='true'
    >
      <svg
        className='relay-obsidian-fallback'
        viewBox='0 0 500 500'
        fill='none'
        focusable='false'
        style={{ opacity: rendered ? 0 : 1 }}
      >
        <defs>
          <linearGradient id={gradientId} x1='104' y1='115' x2='386' y2='379'>
            <stop stopColor='#8c9098' />
            <stop offset='.14' stopColor='#eceef0' />
            <stop offset='.3' stopColor='#1b1d21' />
            <stop offset='.52' stopColor='#0a0b0d' />
            <stop offset='.75' stopColor='#858a93' />
            <stop offset='.87' stopColor='#d6d8de' />
            <stop offset='1' stopColor='#202126' />
          </linearGradient>
        </defs>
        <path
          d='M197 145C237 77 344 83 365 151C387 223 294 292 230 327C173 358 114 310 129 253C146 190 278 171 322 216C368 265 290 365 221 339C155 314 148 226 197 145Z'
          stroke={`url(#${gradientId})`}
          strokeWidth='46'
          strokeLinejoin='round'
        />
        <path
          d='M202 139C239 86 332 93 352 146M138 248C158 205 253 184 297 207M234 330C273 340 312 305 320 278'
          stroke='#ffffff'
          strokeOpacity='.35'
          strokeWidth='3'
          strokeLinecap='round'
        />
      </svg>
      <canvas
        ref={canvasRef}
        className='relay-obsidian-canvas'
        style={{ opacity: rendered ? 1 : 0 }}
      />
    </div>
  )
}
