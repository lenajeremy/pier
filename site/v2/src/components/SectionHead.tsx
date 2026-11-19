import type { ReactNode } from 'react'
import { d, InView } from '../lib/inView'
import s from './SectionHead.module.css'

export function SectionHead({ id, title, lede }: { id: string; title: ReactNode; lede: ReactNode }) {
  return (
    <InView className={s.head}>
      <h2 id={id} className={`${s.title} m-rise`}>
        {title}
      </h2>
      <p className={`${s.lede} m-rise`} style={d(100)}>
        {lede}
      </p>
    </InView>
  )
}
