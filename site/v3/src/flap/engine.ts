// Split-flap tiles, the page's one rule: anything that changes is set in
// tiles and changes only by flipping. A flip drops the upper half of the old
// character and lands the lower half of the new one. No bounce: ease-in for
// the fall, ease-out for the landing.

export const CHARS = ' ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-/:+@'

export const reducedMotion = () =>
  typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches

const wait = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))

export class Tile {
  el: HTMLDivElement
  ch = ' '
  /** Where the tile is heading: the last character asked for. */
  want = ' '
  private spans: HTMLSpanElement[]
  private front: HTMLDivElement
  private back: HTMLDivElement
  private queue: Promise<void> = Promise.resolve()
  private token = 0

  constructor(parent: HTMLElement) {
    this.el = document.createElement('div')
    this.el.className = 'tile blank'
    this.el.innerHTML =
      '<div class="half top"><span></span></div><div class="half bottom"><span></span></div>' +
      '<div class="flap front"><span></span></div><div class="flap back"><span></span></div>'
    this.spans = Array.from(this.el.querySelectorAll('span'))
    this.front = this.el.querySelector('.front') as HTMLDivElement
    this.back = this.el.querySelector('.back') as HTMLDivElement
    parent.appendChild(this.el)
  }

  ink(color?: string) {
    if (color) this.el.style.setProperty('--ink', color)
    else this.el.style.removeProperty('--ink')
  }

  set(ch: string) {
    this.want = ch
    this.show(ch)
  }

  /** Paint a character without changing where the tile is heading. */
  private show(ch: string) {
    this.ch = ch
    const t = ch === ' ' ? '' : ch
    for (const s of this.spans) s.textContent = t
    this.el.classList.toggle('blank', ch === ' ')
  }

  private async step(next: string, ms: number) {
    const [top, bottom, front, back] = this.spans
    const show = (c: string) => (c === ' ' ? '' : c)
    top.textContent = show(next)
    bottom.textContent = show(this.ch)
    front.textContent = show(this.ch)
    back.textContent = show(next)
    this.el.classList.add('flipping')
    const fall = this.front.animate(
      [
        { transform: 'rotateX(0deg)', filter: 'brightness(1)' },
        { transform: 'rotateX(-90deg)', filter: 'brightness(1.6)' },
      ],
      { duration: ms, easing: 'cubic-bezier(.55,0,1,.45)', fill: 'forwards' },
    )
    await fall.finished
    const land = this.back.animate(
      [
        { transform: 'rotateX(90deg)', filter: 'brightness(.55)' },
        { transform: 'rotateX(0deg)', filter: 'brightness(1)' },
      ],
      { duration: ms, easing: 'cubic-bezier(0,.55,.45,1)', fill: 'forwards' },
    )
    await land.finished
    fall.cancel()
    land.cancel()
    this.el.classList.remove('flipping')
    this.show(next)
  }

  /** Flip to `target`. `extra` random characters pass first; null walks the
   * real path through the character set. A newer call cancels an older one. */
  flipTo(target: string, { extra = 4 as number | null, ms = 64, delay = 0 } = {}) {
    const my = ++this.token
    this.want = target
    this.queue = this.queue.then(async () => {
      if (delay) await wait(delay)
      if (my !== this.token) return
      if (reducedMotion() || target === this.ch) return this.set(target)
      const path: string[] = []
      if (extra === null) {
        let i = Math.max(0, CHARS.indexOf(this.ch))
        const t = Math.max(0, CHARS.indexOf(target))
        while (i !== t) {
          i = (i + 1) % CHARS.length
          path.push(CHARS[i])
        }
      } else {
        for (let k = 0; k < extra; k++) path.push(CHARS[1 + Math.floor(Math.random() * (CHARS.length - 1))])
        path.push(target)
      }
      for (const c of path) {
        if (my !== this.token) return
        await this.step(c, ms)
      }
      if (my !== this.token) return
      this.want = target
    })
    return this.queue
  }
}

export type Column = { width: number; ink?: string }

/** A row of tiles in fixed-width columns, with a gap between columns. */
export class FlapRow {
  el: HTMLDivElement
  cols: Tile[][] = []

  constructor(parent: HTMLElement, columns: Column[]) {
    this.el = document.createElement('div')
    this.el.className = 'flap-row'
    columns.forEach((c, i) => {
      if (i > 0) {
        const g = document.createElement('div')
        g.className = 'flap-gap'
        this.el.appendChild(g)
      }
      const tiles: Tile[] = []
      for (let k = 0; k < c.width; k++) {
        const t = new Tile(this.el)
        if (c.ink) t.ink(c.ink)
        tiles.push(t)
      }
      this.cols.push(tiles)
    })
    parent.appendChild(this.el)
  }

  /** Settle on `values` straight away, no flipping. */
  set(values: string[], inks?: (string | undefined)[]) {
    values.forEach((v, ci) => {
      const tiles = this.cols[ci]
      if (!tiles) return
      const text = pad(v, tiles.length)
      tiles.forEach((t, i) => {
        if (inks) t.ink(inks[ci])
        t.set(text[i])
      })
    })
  }

  /** Flip to `values`, cascading left to right. Only tiles that change flip. */
  show(
    values: string[],
    { stagger = 24, extra = 4 as number | null, ms = 62, start = 0, inks }: {
      stagger?: number
      extra?: number | null
      ms?: number
      start?: number
      inks?: (string | undefined)[]
    } = {},
  ) {
    const jobs: Promise<void>[] = []
    let col = 0
    values.forEach((v, ci) => {
      const tiles = this.cols[ci]
      if (!tiles) return
      const text = pad(v, tiles.length)
      tiles.forEach((t, i) => {
        if (inks && ci in inks) t.ink(inks[ci])
        if (t.want === text[i]) return
        const e = extra === null ? null : text[i] === ' ' ? 1 : extra + Math.floor(Math.random() * 3)
        jobs.push(t.flipTo(text[i], { extra: e, ms, delay: start + (col + i) * stagger }))
      })
      col += tiles.length + 1
    })
    return Promise.all(jobs)
  }
}

const pad = (v: string, n: number) => v.toUpperCase().padEnd(n, ' ').slice(0, n)
