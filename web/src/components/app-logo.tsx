import React from 'react'
import '../styles/logo.css'
import { Link, type LinkProps } from 'react-router-dom'
import { cn } from '@/lib/utils'

export function LogoSprite() {
  return (
    <svg style={{ display: 'none' }}>
      <symbol id="winterflow-asterisk" viewBox="0 0 24 24">
        <g stroke="currentColor" strokeWidth="3.8" strokeLinecap="round">
          <line x1="12" y1="4" x2="12" y2="20" transform="rotate(30, 12, 12)" />
          <line x1="12" y1="4" x2="12" y2="20" transform="rotate(90, 12, 12)" />
          <line x1="12" y1="4" x2="12" y2="20" transform="rotate(150, 12, 12)" />
        </g>
      </symbol>
    </svg>
  )
}

interface LogoIconProps extends React.ComponentPropsWithoutRef<'svg'> {
  size?: 'sm' | 'md' | 'lg' | 'xl'
}

export function LogoIcon({ size = 'md', className, ...props }: LogoIconProps) {
  const sizeClasses = {
    sm: 'h-6 w-6',
    md: 'h-8 w-8',
    lg: 'h-10 w-10',
    xl: 'h-12 w-12',
  }

  return (
    <svg
      className={`text-blue-400 ${sizeClasses[size]} ${className || ''}`}
      aria-hidden="true"
      {...props}
    >
      <use href="#winterflow-asterisk" />
    </svg>
  )
}

type LogoSpinnerProps = {
  size?: React.ComponentProps<typeof LogoIcon>['size']
  containerClassName?: string
  iconClassName?: string
}

export function LogoSpinner({
  size = 'md',
  containerClassName,
  iconClassName,
}: LogoSpinnerProps) {
  const [isSpinning, setIsSpinning] = React.useState(false)

  return (
    <div
      onMouseEnter={() => {
        if (!isSpinning) {
          setIsSpinning(true)
        }
      }}
      onAnimationEnd={() => setIsSpinning(false)}
      className={cn(
        'logo-spin-hover',
        containerClassName,
        isSpinning && 'logo-spin-active',
      )}
    >
      <LogoIcon size={size} className={iconClassName} />
    </div>
  )
}

interface LogoTextProps extends React.ComponentPropsWithoutRef<'span'> {
  size?: 'sm' | 'md' | 'lg' | 'xl'
}

export function LogoText({ size = 'md', className, ...props }: LogoTextProps) {
  const sizeClasses = {
    sm: 'text-lg',
    md: 'text-2xl',
    lg: 'text-3xl',
    xl: 'text-4xl',
  }

  return (
    <span className={`logo-text whitespace-nowrap ${sizeClasses[size]} ${className || ''}`} {...props}>
      <span className="logo-winter">Winter</span>
      <span className="logo-flow">Flow</span>
      <span className="logo-io">.io</span>
    </span>
  )
}

interface LogoProps extends Omit<LinkProps, 'to'> {
  to?: LinkProps['to']
  iconOnly?: boolean
  size?: 'sm' | 'md' | 'lg' | 'xl'
}

export function Logo({ iconOnly = false, size = 'md', className, to = '/', ...props }: LogoProps) {
  return (
    <Link to={to} className={`logo-container !m-0 !p-0 ${className || ''}`} {...props}>
      <span className="sr-only">WinterFlow.io</span>
      <LogoIcon size={size} />
      {!iconOnly && <LogoText size={size} className="ml-2" />}
    </Link>
  )
} 
