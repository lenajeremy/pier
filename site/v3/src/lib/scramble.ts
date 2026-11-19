import { useEffect, useRef, useState } from 'react'
import { motionOn } from './motion'

const GLYPHS = 'abcdef0123456789'

/** Text that resolves character by character from noise to `value` whenever
 * `value` changes, the way a hash settles. Plain text without motion. */
export function useScramble(value: string, { speed = 34 } = {}) {
  const [text, setText] = useState(value)
  const first = useRef(true)
  useEffect(() => {
    if (first.current || !motionOn()) {
      first.current = false
      setText(value)
      return
    }
    let frame = 0
    const total = value.length * 3 + 6
    const id = setInterval(() => {
      frame++
      const fixed = Math.floor((frame - 6) / 3)
      setText(
        value
          .split('')
          .map((c, i) => (i < fixed ? c : GLYPHS[Math.floor(Math.random() * GLYPHS.length)]))
          .join(''),
      )
      if (frame >= total) {
        clearInterval(id)
        setText(value)
      }
    }, speed)
    return () => clearInterval(id)
  }, [value, speed])
  return text
}

/** Text typed out once `play` turns true. */
export function useTyped(value: string, play: boolean, { speed = 28, delay = 0 } = {}) {
  const [n, setN] = useState(motionOn() ? 0 : value.length)
  useEffect(() => {
    if (!play || !motionOn()) return
    let i = 0
    let id: ReturnType<typeof setInterval>
    const start = setTimeout(() => {
      id = setInterval(() => {
        i++
        setN(i)
        if (i >= value.length) clearInterval(id)
      }, speed)
    }, delay)
    return () => {
      clearTimeout(start)
      clearInterval(id)
    }
  }, [play, value, speed, delay])
  return value.slice(0, n)
}
