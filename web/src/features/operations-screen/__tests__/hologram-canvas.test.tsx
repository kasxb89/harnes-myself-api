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
import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { HologramCanvas } from '../components/hologram-canvas'

const gpu = vi.hoisted(() => ({ create: vi.fn() }))

// jsdom cannot allocate a GPU. Keep the real React owner and replace only its
// hardware boundary; the owner must still manage visibility, time and cleanup.
vi.mock('../components/hologram-scene', () => ({
  createHologramScene: gpu.create,
}))

const scene = {
  resize: vi.fn<(width: number, height: number) => void>(),
  render: vi.fn<(elapsedSeconds: number) => void>(),
  dispose: vi.fn<() => void>(),
}
let intersections: {
  callback: IntersectionObserverCallback
  observe: ReturnType<typeof vi.fn>
  disconnect: ReturnType<typeof vi.fn>
}[]
let resizes: {
  callback: ResizeObserverCallback
  observe: ReturnType<typeof vi.fn>
  disconnect: ReturnType<typeof vi.fn>
}[]
let frames: Map<number, FrameRequestCallback>
let frameId: number
let width: number
let height: number
let reduced: boolean
let visible: DocumentVisibilityState
let mediaListeners: Set<EventListenerOrEventListenerObject>

beforeEach(() => {
  intersections = []
  resizes = []
  frames = new Map()
  frameId = 0
  width = 480
  height = 600
  reduced = false
  visible = 'visible'
  mediaListeners = new Set()
  gpu.create.mockReset().mockReturnValue(scene)
  scene.resize.mockReset()
  scene.render.mockReset()
  scene.dispose.mockReset()
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
    () => new DOMRect(0, 0, width, height)
  )
  vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visible)
  vi.spyOn(document, 'hidden', 'get').mockImplementation(
    () => visible !== 'visible'
  )
  vi.stubGlobal(
    'IntersectionObserver',
    class {
      observe = vi.fn()
      unobserve = vi.fn()
      disconnect = vi.fn()
      constructor(callback: IntersectionObserverCallback) {
        intersections.push({
          callback,
          observe: this.observe,
          disconnect: this.disconnect,
        })
      }
    }
  )
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe = vi.fn()
      unobserve = vi.fn()
      disconnect = vi.fn()
      constructor(callback: ResizeObserverCallback) {
        resizes.push({
          callback,
          observe: this.observe,
          disconnect: this.disconnect,
        })
      }
    }
  )
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    matches: reduced,
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: (
      _type: string,
      listener: EventListenerOrEventListenerObject | null
    ) => {
      if (listener) mediaListeners.add(listener)
    },
    removeEventListener: (
      _type: string,
      listener: EventListenerOrEventListenerObject | null
    ) => {
      if (listener) mediaListeners.delete(listener)
    },
    dispatchEvent: () => true,
  }))
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
    const id = ++frameId
    frames.set(id, callback)
    return id
  })
  vi.spyOn(window, 'cancelAnimationFrame').mockImplementation((id) => {
    frames.delete(id)
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function intersect(isIntersecting: boolean): void {
  for (const observer of intersections) {
    const target = observer.observe.mock.calls[0]?.[0] as Element
    observer.callback(
      [{ isIntersecting, target } as IntersectionObserverEntry],
      {} as IntersectionObserver
    )
  }
}

function resize(nextWidth: number, nextHeight: number): void {
  width = nextWidth
  height = nextHeight
  for (const observer of resizes) {
    const target = observer.observe.mock.calls[0]?.[0] as Element
    observer.callback(
      [
        {
          target,
          contentRect: new DOMRect(0, 0, width, height),
        } as ResizeObserverEntry,
      ],
      {} as ResizeObserver
    )
  }
}

function frame(timestamp: number): void {
  const callbacks = [...frames.values()]
  frames.clear()
  act(() => {
    for (const callback of callbacks) callback(timestamp)
  })
}

async function enterViewport(): Promise<void> {
  await act(async () => {
    intersect(true)
  })
}

it('does not allocate a hidden or zero-size GPU and ignores an import that settles after unmount', async () => {
  width = 0
  height = 0
  const view = render(<HologramCanvas paused={false} />)
  expect(gpu.create).not.toHaveBeenCalled()
  await enterViewport()
  expect(gpu.create).not.toHaveBeenCalled()
  act(() => {
    resize(480, 600)
  })
  view.unmount()
  await act(async () => {})
  expect(gpu.create).not.toHaveBeenCalled()
  expect(frames.size).toBe(0)
})

it('keeps the projection decorative while resizing and reusing its scene across viewport and zero-size changes', async () => {
  const view = render(<HologramCanvas paused={false} />)
  await enterViewport()
  expect(gpu.create).toHaveBeenCalledTimes(1)
  const canvas = view.container.querySelector('canvas')
  expect(canvas?.closest('[aria-hidden="true"]')).not.toBeNull()
  expect(canvas).not.toHaveAttribute('tabindex')
  expect(screen.queryByRole('img')).not.toBeInTheDocument()
  expect(screen.queryByRole('button')).not.toBeInTheDocument()
  expect(scene.resize).toHaveBeenCalledWith(480, 600)
  frame(0)
  frame(40)
  expect(scene.render).toHaveBeenCalled()
  act(() => resize(0, 0))
  const stoppedAt = scene.render.mock.calls.length
  frame(80)
  expect(scene.render).toHaveBeenCalledTimes(stoppedAt)
  expect(frames.size).toBe(0)
  act(() => resize(800, 450))
  frame(120)
  frame(160)
  expect(scene.resize).toHaveBeenLastCalledWith(800, 450)
  expect(scene.render.mock.calls.length).toBeGreaterThan(stoppedAt)
  act(() => intersect(false))
  const offscreenAt = scene.render.mock.calls.length
  frame(200)
  expect(scene.render).toHaveBeenCalledTimes(offscreenAt)
  expect(frames.size).toBe(0)
  await enterViewport()
  frame(240)
  frame(280)
  expect(scene.render.mock.calls.length).toBeGreaterThan(offscreenAt)
  expect(gpu.create).toHaveBeenCalledTimes(1)
  view.unmount()
  expect(scene.dispose).toHaveBeenCalledTimes(1)
  expect(frames.size).toBe(0)
  for (const observer of [...intersections, ...resizes]) {
    expect(observer.disconnect).toHaveBeenCalledTimes(1)
  }
})

it.each(['paused', 'reduced motion', 'hidden document'] as const)(
  'freezes the existing scene for %s and resumes without advancing by time spent suspended',
  async (reason) => {
    const view = render(<HologramCanvas paused={false} />)
    await enterViewport()
    frame(0)
    frame(40)
    const beforePause = scene.render.mock.lastCall?.[0] ?? 0
    act(() => {
      if (reason === 'paused') view.rerender(<HologramCanvas paused />)
      if (reason === 'hidden document') {
        visible = 'hidden'
        document.dispatchEvent(new Event('visibilitychange'))
      }
      if (reason === 'reduced motion') {
        reduced = true
        for (const listener of mediaListeners) {
          const event = new Event('change')
          if (typeof listener === 'function') listener(event)
          else listener.handleEvent(event)
        }
      }
    })
    const pausedAt = scene.render.mock.calls.length
    frame(600_000)
    expect(scene.render).toHaveBeenCalledTimes(pausedAt)
    expect(frames.size).toBe(0)
    act(() => {
      if (reason === 'paused') view.rerender(<HologramCanvas paused={false} />)
      if (reason === 'hidden document') {
        visible = 'visible'
        document.dispatchEvent(new Event('visibilitychange'))
      }
      if (reason === 'reduced motion') {
        reduced = false
        for (const listener of mediaListeners) {
          const event = new Event('change')
          if (typeof listener === 'function') listener(event)
          else listener.handleEvent(event)
        }
      }
    })
    frame(600_040)
    frame(600_080)
    expect(scene.render.mock.calls.length).toBeGreaterThan(pausedAt)
    expect(scene.render.mock.lastCall?.[0]).toBeLessThanOrEqual(
      beforePause + 0.1
    )
    expect(gpu.create).toHaveBeenCalledTimes(1)
  }
)

it('limits continuous redraws to thirty frames per second', async () => {
  render(<HologramCanvas paused={false} />)
  await enterViewport()
  scene.render.mockClear()
  for (const time of [0, 8, 16, 24, 32, 40, 48, 56, 64, 72, 80]) {
    frame(time)
  }
  expect(scene.render.mock.calls.length).toBeGreaterThan(0)
  expect(scene.render.mock.calls.length).toBeLessThanOrEqual(3)
})

it('falls back to a static decorative projection when WebGL initialization is unavailable', async () => {
  gpu.create.mockImplementation(() => {
    throw new Error('WebGL unavailable')
  })
  const view = render(<HologramCanvas paused={false} />)
  await enterViewport()
  expect(view.container.querySelector('.ops-hud-projection')).toHaveAttribute(
    'data-renderer',
    'fallback'
  )
  expect(
    view.container
      .querySelector('.ops-reactor-fallback')
      ?.closest('[aria-hidden="true"]')
  ).not.toBeNull()
  expect(frames.size).toBe(0)
  expect(scene.render).not.toHaveBeenCalled()
})

it('disposes a lost WebGL scene and replaces it only after the browser restores its context', async () => {
  const restoredScene = {
    resize: vi.fn<(width: number, height: number) => void>(),
    render: vi.fn<(elapsedSeconds: number) => void>(),
    dispose: vi.fn<() => void>(),
  }
  gpu.create.mockReturnValueOnce(scene).mockReturnValueOnce(restoredScene)
  const view = render(<HologramCanvas paused={false} />)
  await enterViewport()
  const canvas = view.container.querySelector('canvas')
  expect(canvas).not.toBeNull()
  act(() =>
    canvas?.dispatchEvent(new Event('webglcontextlost', { cancelable: true }))
  )
  expect(view.container.querySelector('.ops-hud-projection')).toHaveAttribute(
    'data-renderer',
    'fallback'
  )
  const lostAt = scene.render.mock.calls.length
  frame(200)
  expect(scene.render).toHaveBeenCalledTimes(lostAt)
  expect(frames.size).toBe(0)
  expect(scene.dispose).toHaveBeenCalledTimes(1)
  act(() => canvas?.dispatchEvent(new Event('webglcontextrestored')))
  expect(gpu.create).toHaveBeenCalledTimes(2)
  expect(view.container.querySelector('.ops-hud-projection')).toHaveAttribute(
    'data-renderer',
    'webgl'
  )
  expect(restoredScene.resize).toHaveBeenCalledWith(480, 600)
  expect(restoredScene.render).toHaveBeenCalled()
  view.unmount()
  expect(scene.dispose).toHaveBeenCalledTimes(1)
  expect(restoredScene.dispose).toHaveBeenCalledTimes(1)
})
