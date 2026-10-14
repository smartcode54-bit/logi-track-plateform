import * as React from "react"

const MOBILE_BREAKPOINT = 768
const LG_BREAKPOINT = 1024
const mediaQueryLists = new Map<string, MediaQueryList>()

function getMediaQueryList(query: string) {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return undefined
  }

  let mediaQueryList = mediaQueryLists.get(query)
  if (!mediaQueryList) {
    mediaQueryList = window.matchMedia(query)
    mediaQueryLists.set(query, mediaQueryList)
  }

  return mediaQueryList
}

function subscribeToMediaQuery(query: string, onChange: () => void) {
  const mediaQueryList = getMediaQueryList(query)
  if (!mediaQueryList) return () => {}

  mediaQueryList.addEventListener("change", onChange)
  return () => mediaQueryList.removeEventListener("change", onChange)
}

function getMediaQuerySnapshot(query: string) {
  return getMediaQueryList(query)?.matches ?? false
}

const getServerSnapshot = () => false

export function useIsMobile() {
  const query = `(max-width: ${MOBILE_BREAKPOINT - 1}px)`

  return React.useSyncExternalStore(
    (onChange) => subscribeToMediaQuery(query, onChange),
    () => getMediaQuerySnapshot(query),
    getServerSnapshot,
  )
}

/** True when viewport is >= lg (1024px). Use for Command Center: desktop = 3 columns, below = drawers. */
export function useIsLg() {
  const query = `(min-width: ${LG_BREAKPOINT}px)`

  return React.useSyncExternalStore(
    (onChange) => subscribeToMediaQuery(query, onChange),
    () => getMediaQuerySnapshot(query),
    getServerSnapshot,
  )
}
