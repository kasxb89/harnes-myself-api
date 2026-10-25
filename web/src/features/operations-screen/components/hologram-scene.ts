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
  ACESFilmicToneMapping,
  AdditiveBlending,
  BoxGeometry,
  Color,
  DataTexture,
  DirectionalLight,
  DoubleSide,
  Group,
  HemisphereLight,
  InstancedMesh,
  LinearFilter,
  LinearMipmapLinearFilter,
  Matrix4,
  Mesh,
  MeshStandardMaterial,
  Object3D,
  PerspectiveCamera,
  PlaneGeometry,
  RGBAFormat,
  Scene,
  ShaderMaterial,
  SphereGeometry,
  SRGBColorSpace,
  Vector3,
  WebGLRenderer,
} from 'three'

export interface HologramScene {
  resize(width: number, height: number): void
  render(elapsedSeconds: number): void
  dispose(): void
}

interface Disposable {
  dispose(): void
}

type Coordinate = readonly [longitude: number, latitude: number]

// Hand-authored, deliberately approximate geographic outlines. These are an
// original decorative model, not a downloaded map, geopolitical or live dataset.
const LAND_OUTLINES: readonly (readonly Coordinate[])[] = [
  [
    [-168, 71],
    [-154, 70],
    [-145, 60],
    [-134, 58],
    [-128, 50],
    [-124, 42],
    [-117, 32],
    [-111, 31],
    [-108, 24],
    [-104, 20],
    [-98, 16],
    [-92, 15],
    [-88, 18],
    [-87, 21],
    [-82, 22],
    [-81, 25],
    [-83, 29],
    [-89, 29],
    [-95, 29],
    [-97, 26],
    [-97, 28],
    [-91, 30],
    [-85, 30],
    [-81, 32],
    [-79, 35],
    [-75, 36],
    [-74, 41],
    [-68, 45],
    [-60, 46],
    [-54, 53],
    [-61, 59],
    [-66, 60],
    [-70, 58],
    [-77, 55],
    [-82, 61],
    [-83, 66],
    [-92, 69],
    [-101, 73],
    [-118, 75],
    [-133, 70],
    [-142, 69],
    [-151, 72],
    [-162, 72],
  ],
  [
    [-92, 18],
    [-87, 15],
    [-83, 15],
    [-83, 10],
    [-78, 8],
    [-77, 5],
    [-81, 8],
    [-84, 10],
    [-88, 14],
  ],
  [
    [-81, 12],
    [-72, 11],
    [-66, 10],
    [-60, 7],
    [-51, 4],
    [-48, -1],
    [-35, -6],
    [-37, -13],
    [-40, -19],
    [-44, -23],
    [-48, -28],
    [-52, -33],
    [-56, -38],
    [-63, -42],
    [-67, -49],
    [-69, -55],
    [-73, -53],
    [-75, -45],
    [-74, -35],
    [-72, -29],
    [-70, -18],
    [-76, -10],
    [-80, -5],
    [-81, 2],
    [-77, 7],
  ],
  [
    [-52, 60],
    [-44, 61],
    [-38, 67],
    [-22, 72],
    [-18, 78],
    [-30, 83],
    [-47, 83],
    [-59, 77],
    [-55, 69],
  ],
  [
    [-17, 37],
    [-7, 36],
    [1, 36],
    [10, 37],
    [15, 32],
    [23, 32],
    [31, 31],
    [34, 28],
    [36, 22],
    [43, 12],
    [51, 12],
    [46, 5],
    [42, -1],
    [40, -10],
    [36, -18],
    [35, -25],
    [29, -31],
    [19, -35],
    [16, -29],
    [12, -17],
    [9, -5],
    [8, 4],
    [1, 5],
    [-5, 5],
    [-10, 7],
    [-16, 12],
    [-17, 21],
    [-13, 28],
  ],
  [
    [-10, 36],
    [-9, 43],
    [-2, 44],
    [3, 48],
    [8, 54],
    [11, 56],
    [5, 59],
    [8, 64],
    [15, 69],
    [25, 71],
    [31, 70],
    [29, 61],
    [37, 65],
    [43, 69],
    [58, 71],
    [73, 73],
    [90, 77],
    [110, 77],
    [127, 72],
    [145, 72],
    [162, 68],
    [179, 66],
    [179, 58],
    [164, 60],
    [162, 54],
    [156, 51],
    [153, 58],
    [143, 59],
    [135, 53],
    [141, 46],
    [134, 42],
    [129, 43],
    [127, 39],
    [122, 40],
    [121, 32],
    [119, 25],
    [113, 22],
    [109, 19],
    [108, 12],
    [104, 9],
    [102, 2],
    [99, 7],
    [98, 16],
    [94, 20],
    [91, 22],
    [87, 21],
    [83, 17],
    [79, 9],
    [76, 8],
    [72, 18],
    [68, 23],
    [61, 25],
    [57, 25],
    [52, 17],
    [44, 13],
    [42, 17],
    [35, 30],
    [33, 34],
    [36, 36],
    [31, 41],
    [27, 40],
    [24, 36],
    [22, 39],
    [19, 40],
    [17, 42],
    [16, 39],
    [13, 42],
    [10, 44],
    [7, 43],
    [3, 42],
    [0, 39],
    [-3, 36],
  ],
  [
    [-8, 50],
    [-5, 50],
    [1, 53],
    [-2, 56],
    [-3, 59],
    [-6, 58],
  ],
  [
    [-10, 51],
    [-6, 51],
    [-6, 55],
    [-9, 55],
  ],
  [
    [-24, 64],
    [-13, 64],
    [-14, 67],
    [-21, 67],
  ],
  [
    [130, 31],
    [134, 34],
    [139, 36],
    [142, 41],
    [145, 45],
    [141, 44],
    [138, 38],
    [132, 34],
  ],
  [
    [119, 22],
    [122, 25],
    [122, 21],
  ],
  [
    [95, 5],
    [101, 1],
    [105, -6],
    [102, -7],
    [98, -1],
  ],
  [
    [108, 7],
    [118, 7],
    [119, 0],
    [115, -4],
    [109, -3],
  ],
  [
    [105, -6],
    [114, -7],
    [116, -9],
    [108, -8],
  ],
  [
    [119, -2],
    [122, 2],
    [125, 1],
    [123, -4],
    [121, -5],
  ],
  [
    [130, -2],
    [140, -3],
    [151, -6],
    [148, -10],
    [140, -8],
    [134, -7],
  ],
  [
    [113, -22],
    [115, -16],
    [122, -14],
    [127, -15],
    [132, -11],
    [137, -12],
    [139, -17],
    [145, -15],
    [146, -21],
    [153, -26],
    [151, -33],
    [146, -39],
    [140, -36],
    [137, -35],
    [130, -33],
    [123, -34],
    [116, -31],
  ],
  [
    [144, -41],
    [149, -41],
    [147, -44],
    [145, -44],
  ],
  [
    [173, -35],
    [178, -38],
    [176, -41],
    [173, -39],
  ],
  [
    [173, -41],
    [173, -44],
    [167, -47],
    [166, -45],
  ],
  [
    [44, -13],
    [50, -15],
    [47, -25],
    [44, -24],
  ],
  [
    [-84, 22],
    [-74, 20],
    [-78, 19],
    [-84, 21],
  ],
  [
    [-73, 20],
    [-68, 19],
    [-69, 18],
    [-73, 18],
  ],
]

const LAND_POLYGONS = LAND_OUTLINES.map((points) => {
  let west = 180
  let east = -180
  let south = 90
  let north = -90
  for (const point of points) {
    west = Math.min(west, point[0])
    east = Math.max(east, point[0])
    south = Math.min(south, point[1])
    north = Math.max(north, point[1])
  }
  return { points, west, east, south, north }
})

function geographicLand(longitude: number, latitude: number): boolean {
  if (latitude < -73 + Math.sin(longitude * 0.075) * 3) return true
  for (const polygon of LAND_POLYGONS) {
    if (
      longitude < polygon.west ||
      longitude > polygon.east ||
      latitude < polygon.south ||
      latitude > polygon.north
    ) {
      continue
    }
    const outline = polygon.points
    let inside = false
    for (
      let index = 0, previous = outline.length - 1;
      index < outline.length;
      previous = index++
    ) {
      const first = outline[index]
      const second = outline[previous]
      if (!first || !second) continue
      if (
        first[1] > latitude !== second[1] > latitude &&
        longitude <
          ((second[0] - first[0]) * (latitude - first[1])) /
            (second[1] - first[1]) +
            first[0]
      ) {
        inside = !inside
      }
    }
    if (inside) return true
  }
  return false
}

/** An original, entirely local decorative earth and surrounding HUD screens. */
export function createHologramScene(canvas: HTMLCanvasElement): HologramScene {
  const resources: Disposable[] = []
  let disposed = false
  const own = <T extends Disposable>(resource: T): T => {
    resources.push(resource)
    return resource
  }
  const dispose = (): void => {
    if (disposed) return
    disposed = true
    for (let index = resources.length - 1; index >= 0; index--) {
      try {
        resources[index]?.dispose()
      } catch {
        // One failed GPU resource must not block renderer cleanup or conceal
        // an original construction failure. Repeated disposal remains harmless.
      }
    }
    resources.length = 0
  }

  try {
    const renderer = own(
      new WebGLRenderer({
        canvas,
        alpha: true,
        antialias: true,
        powerPreference: 'low-power',
        depth: true,
        stencil: false,
      })
    )
    renderer.setClearColor(0x000000, 0)
    renderer.outputColorSpace = SRGBColorSpace
    renderer.toneMapping = ACESFilmicToneMapping
    renderer.toneMappingExposure = 1.12
    renderer.setPixelRatio(1.25)

    const scene = new Scene()
    const camera = new PerspectiveCamera(34, 1, 0.1, 30)
    camera.position.set(0.2, 0.65, 7.4)
    camera.lookAt(0, 0, 0)
    scene.add(new HemisphereLight(0x3a86ac, 0x020611, 0.36))
    const sunlight = new DirectionalLight(0xe0f4ff, 3.4)
    sunlight.position.set(-3.5, 2.2, 4)
    scene.add(sunlight)
    const blueRim = new DirectionalLight(0x157dca, 0.5)
    blueRim.position.set(2, -0.2, -3)
    scene.add(blueRim)

    const textureWidth = 512
    const textureHeight = 256
    const surfacePixels = new Uint8Array(textureWidth * textureHeight * 4)
    const reliefPixels = new Uint8Array(textureWidth * textureHeight * 4)
    const cloudPixels = new Uint8Array(textureWidth * textureHeight * 4)
    const landMask = new Uint8Array(textureWidth * textureHeight)
    for (let y = 0; y < textureHeight; y++) {
      const latitude = -90 + (y / (textureHeight - 1)) * 180
      for (let x = 0; x < textureWidth; x++) {
        const longitude = (x / (textureWidth - 1)) * 360 - 180
        landMask[y * textureWidth + x] = geographicLand(longitude, latitude)
          ? 1
          : 0
      }
    }
    for (let y = 0; y < textureHeight; y++) {
      const latitude = -90 + (y / (textureHeight - 1)) * 180
      for (let x = 0; x < textureWidth; x++) {
        const longitude = (x / (textureWidth - 1)) * 360 - 180
        const offset = (y * textureWidth + x) * 4
        const land = landMask[y * textureWidth + x] === 1
        const detail =
          Math.sin(longitude * 0.87 + Math.sin(latitude * 0.55) * 2) *
            Math.sin(latitude * 1.17 + Math.sin(longitude * 0.39)) *
            0.5 +
          Math.sin(longitude * 2.2 - latitude * 1.9) * 0.2
        let red = 8 + detail * 3
        let green = 34 + detail * 7
        let blue = 64 + detail * 10
        let relief = 23
        if (land) {
          const polar = Math.max(0, Math.min(1, (Math.abs(latitude) - 54) / 19))
          const desert =
            (latitude > 12 &&
              latitude < 32 &&
              longitude > -18 &&
              longitude < 64) ||
            (latitude < -18 &&
              latitude > -32 &&
              longitude > 118 &&
              longitude < 143)
          const andes =
            latitude < 8 && latitude > -52
              ? Math.exp(-Math.pow((longitude + 71) / 2.2, 2))
              : 0
          const himalaya =
            longitude > 70 && longitude < 105
              ? Math.exp(-Math.pow((latitude - 31) / 2.5, 2))
              : 0
          const rockies =
            latitude > 28 && latitude < 59
              ? Math.exp(-Math.pow((longitude + 115) / 3, 2))
              : 0
          const altitude = Math.max(andes, himalaya, rockies) * 0.7
          red = desert ? 152 : 48
          green = desert ? 125 : 88
          blue = desert ? 78 : 63
          red += detail * 15 + altitude * 90
          green += detail * 19 + altitude * 69
          blue += detail * 10 + altitude * 60
          red = red * (1 - polar) + 205 * polar
          green = green * (1 - polar) + 222 * polar
          blue = blue * (1 - polar) + 227 * polar
          relief = 91 + detail * 16 + altitude * 112
        } else {
          const east = (x + 2) % textureWidth
          const west = (x - 2 + textureWidth) % textureWidth
          const north = Math.max(0, y - 2)
          const south = Math.min(textureHeight - 1, y + 2)
          if (
            landMask[y * textureWidth + east] ||
            landMask[y * textureWidth + west] ||
            landMask[north * textureWidth + x] ||
            landMask[south * textureWidth + x]
          ) {
            red = 17
            green = 66
            blue = 88
          }
        }
        surfacePixels[offset] = red
        surfacePixels[offset + 1] = green
        surfacePixels[offset + 2] = blue
        surfacePixels[offset + 3] = 255
        reliefPixels[offset] = relief
        reliefPixels[offset + 1] = land ? 235 : 100
        reliefPixels[offset + 2] = relief
        reliefPixels[offset + 3] = 255
        const cloudField =
          Math.sin(longitude * 0.11 + Math.sin(latitude * 0.085) * 3) *
            Math.cos(latitude * 0.24 + Math.sin(longitude * 0.065) * 2) +
          Math.sin(longitude * 0.43 - latitude * 0.31) * 0.28 +
          detail * 0.24
        const cloud = Math.max(0, Math.min(1, (cloudField - 0.35) * 1.6))
        cloudPixels[offset] = 224
        cloudPixels[offset + 1] = 239
        cloudPixels[offset + 2] = 247
        cloudPixels[offset + 3] = cloud * 150
      }
    }
    const surfaceTexture = own(
      new DataTexture(surfacePixels, textureWidth, textureHeight, RGBAFormat)
    )
    surfaceTexture.colorSpace = SRGBColorSpace
    surfaceTexture.needsUpdate = true
    const reliefTexture = own(
      new DataTexture(reliefPixels, textureWidth, textureHeight, RGBAFormat)
    )
    reliefTexture.needsUpdate = true
    const cloudTexture = own(
      new DataTexture(cloudPixels, textureWidth, textureHeight, RGBAFormat)
    )
    cloudTexture.colorSpace = SRGBColorSpace
    cloudTexture.needsUpdate = true
    for (const texture of [surfaceTexture, reliefTexture, cloudTexture]) {
      texture.magFilter = LinearFilter
      texture.minFilter = LinearMipmapLinearFilter
      texture.generateMipmaps = true
    }

    const assembly = new Group()
    scene.add(assembly)
    const planet = new Group()
    planet.rotation.z = 0.12
    planet.position.y = 0.05
    assembly.add(planet)
    const earthGeometry = own(new SphereGeometry(1.05, 64, 48))
    const earthMaterial = own(
      new MeshStandardMaterial({
        map: surfaceTexture,
        bumpMap: reliefTexture,
        bumpScale: 0.028,
        roughnessMap: reliefTexture,
        roughness: 0.85,
        metalness: 0.04,
        emissive: 0x020816,
        emissiveIntensity: 0.35,
      })
    )
    const earth = new Mesh(earthGeometry, earthMaterial)
    earth.rotation.y = -1.92
    planet.add(earth)
    const clouds = new Mesh(
      own(new SphereGeometry(1.065, 56, 40)),
      own(
        new MeshStandardMaterial({
          map: cloudTexture,
          transparent: true,
          opacity: 0.68,
          roughness: 1,
          depthWrite: false,
        })
      )
    )
    clouds.rotation.y = -1.92
    planet.add(clouds)
    const atmosphereMaterial = own(
      new ShaderMaterial({
        uniforms: {
          color: { value: new Color(0x3faaff) },
          lightDirection: { value: sunlight.position.clone().normalize() },
        },
        vertexShader:
          'varying vec3 vWorldNormal; varying vec3 vWorldPosition; void main(){ vec4 world=modelMatrix*vec4(position,1.0); vWorldPosition=world.xyz; vWorldNormal=normalize(mat3(modelMatrix)*normal); gl_Position=projectionMatrix*viewMatrix*world; }',
        fragmentShader:
          'varying vec3 vWorldNormal; varying vec3 vWorldPosition; uniform vec3 color; uniform vec3 lightDirection; void main(){ vec3 n=normalize(vWorldNormal); vec3 eye=normalize(cameraPosition-vWorldPosition); float rim=pow(1.0-max(0.0,dot(n,eye)),3.6); float day=0.25+0.75*max(0.0,dot(n,lightDirection)); gl_FragColor=vec4(color,rim*day*0.48); }',
        transparent: true,
        blending: AdditiveBlending,
        depthWrite: false,
      })
    )
    const atmosphere = new Mesh(
      own(new SphereGeometry(1.105, 56, 40)),
      atmosphereMaterial
    )
    planet.add(atmosphere)

    const frameMaterial = own(
      new MeshStandardMaterial({
        color: 0x0b283e,
        emissive: 0x083047,
        emissiveIntensity: 0.45,
        metalness: 0.6,
        roughness: 0.3,
      })
    )
    const edgeMaterial = own(
      new MeshStandardMaterial({
        color: 0x9be8ff,
        emissive: 0x26aee9,
        emissiveIntensity: 1.8,
        metalness: 0.15,
        roughness: 0.4,
      })
    )
    const screenGeometry = own(new PlaneGeometry(1, 1))
    const frameGeometry = own(new BoxGeometry(1, 1, 1))
    const panelMaterials: ShaderMaterial[] = []
    const panels: Group[] = []
    const panelPlacements: readonly (readonly [
      number,
      number,
      number,
      number,
      number,
      number,
    ])[] = [
      [-1.18, 1.12, -0.1, 0.84, 0.56, 0.4],
      [1.2, 0.86, -0.16, 0.85, 0.61, -0.42],
      [-1.16, -0.98, 0.72, 0.9, 0.62, 0.43],
      [1.12, -1.24, 0.63, 0.84, 0.58, -0.4],
      [0.12, 1.66, -0.58, 0.88, 0.48, -0.15],
    ]
    const panelTransform = new Object3D()
    for (let index = 0; index < panelPlacements.length; index++) {
      const placement = panelPlacements[index]
      if (!placement) continue
      const panel = new Group()
      panel.position.set(placement[0], placement[1], placement[2])
      panel.rotation.set(-0.1, placement[5], index % 2 === 0 ? -0.08 : 0.08)
      panel.scale.set(placement[3], placement[4], 1)
      assembly.add(panel)
      panels.push(panel)
      const frame = own(new InstancedMesh(frameGeometry, frameMaterial, 4))
      const borders: readonly (readonly [number, number, number, number])[] = [
        [0, 0.52, 1.06, 0.035],
        [0, -0.52, 1.06, 0.035],
        [-0.52, 0, 0.035, 1.04],
        [0.52, 0, 0.035, 1.04],
      ]
      for (let border = 0; border < borders.length; border++) {
        const dimensions = borders[border]
        if (!dimensions) continue
        panelTransform.position.set(dimensions[0], dimensions[1], -0.025)
        panelTransform.scale.set(dimensions[2], dimensions[3], 0.055)
        panelTransform.updateMatrix()
        frame.setMatrixAt(border, panelTransform.matrix)
      }
      frame.instanceMatrix.needsUpdate = true
      panel.add(frame)
      const panelMaterial = own(
        new ShaderMaterial({
          uniforms: { time: { value: 0 }, variant: { value: index } },
          vertexShader:
            'varying vec2 vUv; void main(){ vUv=uv; gl_Position=projectionMatrix*modelViewMatrix*vec4(position,1.0); }',
          fragmentShader: [
            'varying vec2 vUv; uniform float time; uniform float variant;',
            'float stroke(float d,float w){return 1.0-smoothstep(w,w+0.009,abs(d));}',
            'void main(){ vec2 uv=vUv; vec2 p=(uv-vec2(0.27,0.55))*vec2(1.0,0.7); float r=length(p);',
            'float rings=stroke(r-0.145,0.003)+stroke(r-0.11,0.002);',
            'float arc=stroke(r-0.176,0.004)*step(-0.4,sin(atan(p.y,p.x)*3.0+variant));',
            'float cross=stroke(p.x,0.0015)*step(abs(p.y),0.055)+stroke(p.y,0.0015)*step(abs(p.x),0.055);',
            'float bars=0.0; for(int i=0;i<4;i++){float fi=float(i); float y=0.72-fi*0.13; float len=0.26+0.06*sin(fi*2.4+variant); bars+=stroke(uv.y-y,0.006)*step(0.55,uv.x)*step(uv.x,0.55+len);}',
            'float wave=stroke(uv.y-(0.15+0.024*sin(uv.x*29.0+variant)+0.012*sin(uv.x*63.0-time*0.25)),0.003)*step(0.1,uv.x)*step(uv.x,0.92);',
            'float grid=(stroke(fract(uv.x*12.0)-0.5,0.005)+stroke(fract(uv.y*8.0)-0.5,0.005))*0.065;',
            'float scan=pow(max(0.0,1.0-abs(uv.y-fract(time*0.045+variant*0.17))*16.0),3.0)*0.1;',
            'float ink=clamp(rings+arc+cross+bars+wave,0.0,1.0); vec3 col=vec3(0.012,0.055,0.085)+vec3(0.1,0.57,0.78)*(ink+grid+scan);',
            'gl_FragColor=vec4(col,0.91); }',
          ].join('\n'),
          transparent: true,
          depthWrite: false,
          side: DoubleSide,
        })
      )
      panelMaterials.push(panelMaterial)
      panel.add(new Mesh(screenGeometry, panelMaterial))
      const accent = new Mesh(frameGeometry, edgeMaterial)
      accent.position.set(-0.33, 0.52, 0.015)
      accent.scale.set(0.3, 0.012, 0.014)
      panel.add(accent)
    }

    // Capture actual geometry in its resting pose. Framing never depends on a
    // particular viewport aspect or on guessed fixed screen-space dimensions.
    const framingPoints: Vector3[] = []
    const instanceMatrix = new Matrix4()
    const worldMatrix = new Matrix4()
    const projected = new Vector3()
    assembly.updateWorldMatrix(true, true)
    assembly.traverse((object) => {
      if (!(object instanceof Mesh)) return
      const positions = object.geometry.getAttribute('position')
      const count = object instanceof InstancedMesh ? object.count : 1
      for (let instance = 0; instance < count; instance++) {
        worldMatrix.copy(object.matrixWorld)
        if (object instanceof InstancedMesh) {
          object.getMatrixAt(instance, instanceMatrix)
          worldMatrix.multiply(instanceMatrix)
        }
        for (let index = 0; index < positions.count; index++) {
          projected.fromBufferAttribute(positions, index)
          framingPoints.push(projected.clone().applyMatrix4(worldMatrix))
        }
      }
    })
    // Include the complete orbit envelope, not only the opening pose, so cards
    // cannot later travel outside a narrow canvas as their depth changes.
    for (const placement of panelPlacements) {
      const radius = Math.hypot(placement[0], placement[2])
      const halfWidth = placement[3] * 0.6
      const halfHeight = placement[4] * 0.58 + 0.06
      for (let step = 0; step < 24; step++) {
        const angle = (step / 24) * Math.PI * 2
        for (const side of [-1, 1]) {
          for (const vertical of [-1, 1]) {
            for (const depth of [-1, 1]) {
              framingPoints.push(
                new Vector3(
                  Math.cos(angle) * radius + side * halfWidth,
                  placement[1] + vertical * halfHeight,
                  Math.sin(angle) * radius + depth * halfWidth * 0.6
                )
              )
            }
          }
        }
      }
    }

    return {
      resize(width: number, height: number): void {
        if (disposed || !Number.isFinite(width) || !Number.isFinite(height)) {
          return
        }
        if (width <= 0 || height <= 0) return
        const scale = Math.min(1, 768 / width, 900 / height)
        renderer.setSize(
          Math.max(1, Math.floor(width * scale)),
          Math.max(1, Math.floor(height * scale)),
          false
        )
        camera.aspect = width / height
        camera.zoom = 1
        camera.clearViewOffset()
        camera.updateProjectionMatrix()
        camera.updateMatrixWorld()
        let horizontalExtent = 0
        let top = -Infinity
        let bottom = Infinity
        for (const point of framingPoints) {
          projected.copy(point).project(camera)
          horizontalExtent = Math.max(horizontalExtent, Math.abs(projected.x))
          top = Math.max(top, projected.y)
          bottom = Math.min(bottom, projected.y)
        }
        const sideMargin = Math.min(15, width * 0.07)
        const topMargin = Math.min(38, height * 0.09)
        const bottomMargin = Math.min(26, height * 0.08)
        camera.zoom = Math.min(
          1.55,
          (1 - (sideMargin * 2) / width) / (horizontalExtent * 1.055),
          (2 - ((topMargin + bottomMargin) * 2) / height) /
            ((top - bottom) * 1.055)
        )
        // Vertically center the formation; leave equal visual space for orbiting cards.
        const targetCenter = (bottomMargin - topMargin) / height
        const center = ((top + bottom) * camera.zoom) / 2
        camera.setViewOffset(
          width,
          height,
          0,
          ((targetCenter - center) * height) / 2,
          width,
          height
        )
      },
      render(elapsedSeconds: number): void {
        if (disposed) return
        const time = Number.isFinite(elapsedSeconds)
          ? Math.max(0, elapsedSeconds)
          : 0
        earth.rotation.y = -1.92 + (time * Math.PI * 2) / 105
        clouds.rotation.y = -1.92 + (time * Math.PI * 2) / 98
        const orbit = time * 0.048
        const orbitCosine = Math.cos(orbit)
        const orbitSine = Math.sin(orbit)
        for (let index = 0; index < panels.length; index++) {
          const panel = panels[index]
          const placement = panelPlacements[index]
          const material = panelMaterials[index]
          if (!panel || !placement || !material) continue
          panel.position.x =
            placement[0] * orbitCosine + placement[2] * orbitSine
          panel.position.z =
            -placement[0] * orbitSine + placement[2] * orbitCosine
          panel.rotation.y = placement[5] + Math.sin(orbit + index * 0.8) * 0.18
          panel.position.y =
            placement[1] + Math.sin(time * 0.32 + index * 1.4) * 0.035
          material.uniforms.time.value = time
        }
        renderer.render(scene, camera)
      },
      dispose,
    }
  } catch (error) {
    dispose()
    throw error
  }
}
