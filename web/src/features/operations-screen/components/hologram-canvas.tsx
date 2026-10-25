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

import { useMediaQuery } from '@/hooks/use-media-query'

import type { HologramScene } from './hologram-scene'

type RendererState = 'loading' | 'webgl' | 'fallback'

/** Decorative rendering has no access to request, balance, or task data. */
export function HologramCanvas(props: { paused: boolean }) {
  const hostRef = useRef<HTMLDivElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const reducedMotion = useMediaQuery('(prefers-reduced-motion: reduce)')
  const motionPaused = useRef(props.paused || reducedMotion)
  const controller = useRef<{ sync: () => void } | null>(null)
  const [rendererState, setRendererState] = useState<RendererState>('loading')

  useEffect(() => {
    motionPaused.current = props.paused || reducedMotion
    controller.current?.sync()
  }, [props.paused, reducedMotion])

  useEffect(() => {
    const host = hostRef.current
    const canvas = canvasRef.current
    if (!host || !canvas) return

    let mounted = true
    let visible = typeof IntersectionObserver === 'undefined'
    let importing = false
    let failed = false
    let contextLost = false
    let scene: HologramScene | undefined
    let factory:
      | typeof import('./hologram-scene').createHologramScene
      | undefined
    let frame = 0
    let elapsed = 0
    let previousPaint: number | undefined
    let renderedWidth = 0
    let renderedHeight = 0

    const stop = () => {
      if (frame) window.cancelAnimationFrame(frame)
      frame = 0
      previousPaint = undefined
    }
    const releaseScene = () => {
      const ownedScene = scene
      scene = undefined
      ownedScene?.dispose()
      renderedWidth = 0
      renderedHeight = 0
    }
    const isVisible = () => {
      const bounds = host.getBoundingClientRect()
      return (
        mounted &&
        visible &&
        document.visibilityState !== 'hidden' &&
        bounds.width > 0 &&
        bounds.height > 0
      )
    }
    const fail = () => {
      failed = true
      stop()
      releaseScene()
      if (mounted) setRendererState('fallback')
    }
    const paint = (timestamp: number) => {
      frame = 0
      if (!scene || failed || motionPaused.current || !isVisible()) {
        previousPaint = undefined
        return
      }
      try {
        // Limit draws to 30 fps, retaining a frozen clock across hidden/paused time.
        if (previousPaint === undefined) previousPaint = timestamp
        if (timestamp - previousPaint >= 1000 / 30 - 0.25) {
          elapsed += Math.min((timestamp - previousPaint) / 1000, 0.05)
          previousPaint = timestamp
          scene.render(elapsed)
        }
        frame = window.requestAnimationFrame(paint)
      } catch {
        fail()
      }
    }
    const sync = () => {
      if (!isVisible() || failed || contextLost) {
        stop()
        return
      }
      if (!factory) {
        if (importing) return
        importing = true
        void import('./hologram-scene').then(
          (module) => {
            importing = false
            if (!mounted) return
            factory = module.createHologramScene
            sync()
          },
          () => {
            importing = false
            if (mounted) fail()
          }
        )
        return
      }
      try {
        if (!scene) {
          scene = factory(canvas)
          setRendererState('webgl')
        }
        const bounds = host.getBoundingClientRect()
        const width = Math.ceil(bounds.width)
        const height = Math.ceil(bounds.height)
        if (width !== renderedWidth || height !== renderedHeight) {
          scene.resize(width, height)
          renderedWidth = width
          renderedHeight = height
          scene.render(elapsed)
        }
        if (motionPaused.current) {
          stop()
        } else if (!frame) {
          frame = window.requestAnimationFrame(paint)
        }
      } catch {
        fail()
      }
    }
    const handleContextLost = (event: Event) => {
      event.preventDefault()
      contextLost = true
      fail()
    }
    const handleContextRestored = () => {
      if (!mounted || !contextLost) return
      contextLost = false
      failed = false
      setRendererState('loading')
      sync()
    }
    const intersection =
      typeof IntersectionObserver === 'undefined'
        ? undefined
        : new IntersectionObserver((entries) => {
            const entry = entries.find((item) => item.target === host)
            if (!entry) return
            visible = entry.isIntersecting
            sync()
          })
    const resize =
      typeof ResizeObserver === 'undefined'
        ? undefined
        : new ResizeObserver(sync)

    controller.current = { sync }
    intersection?.observe(host)
    resize?.observe(host)
    window.addEventListener('resize', sync)
    document.addEventListener('visibilitychange', sync)
    canvas.addEventListener('webglcontextlost', handleContextLost)
    canvas.addEventListener('webglcontextrestored', handleContextRestored)
    sync()

    return () => {
      mounted = false
      controller.current = null
      stop()
      intersection?.disconnect()
      resize?.disconnect()
      window.removeEventListener('resize', sync)
      document.removeEventListener('visibilitychange', sync)
      canvas.removeEventListener('webglcontextlost', handleContextLost)
      canvas.removeEventListener('webglcontextrestored', handleContextRestored)
      releaseScene()
    }
  }, [])

  return (
    <div
      ref={hostRef}
      className='ops-hud-projection'
      data-paused={props.paused || reducedMotion}
      data-renderer={rendererState}
      aria-hidden='true'
    >
      <div className='ops-reactor-canvas'>
        <canvas ref={canvasRef} aria-hidden='true' />
      </div>
      <div className='ops-reactor-fallback' />
    </div>
  )
}
