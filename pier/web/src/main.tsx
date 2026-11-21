import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource/schibsted-grotesk/400.css'
import '@fontsource/schibsted-grotesk/500.css'
import '@fontsource/red-hat-mono/400.css'
import '@fontsource/red-hat-mono/500.css'
import './styles/tokens.css'
import './styles/base.css'
import './styles/components.css'
import './styles/overlays.css'
import './styles/responsive.css'
import { App } from './App'
import { ConfirmHost } from './components/Dialog'
import { ToastHost } from './components/Toast'
import { TooltipHost } from './components/Tooltip'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
    <ConfirmHost />
    <ToastHost />
    <TooltipHost />
  </StrictMode>,
)
