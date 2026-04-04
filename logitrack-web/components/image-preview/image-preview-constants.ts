export const IMAGE_PREVIEW_SCALE_MIN = 0.5;
export const IMAGE_PREVIEW_SCALE_MAX = 4;
export const IMAGE_PREVIEW_SCALE_STEP = 1.25;

/** Ctrl/Meta + wheel: multiply or divide scale by this per tick */
export const IMAGE_PREVIEW_WHEEL_ZOOM_FACTOR = 1.09;

/** Clear pan when scale is at or below this (slightly above 1 for float noise) */
export const IMAGE_PREVIEW_SCALE_IDENTITY_UPPER = 1.001;

/** Treat as zoomed: disable touch slide-change; enable pan cursor threshold */
export const IMAGE_PREVIEW_ZOOMED_NAV_THRESHOLD = 1.02;

/** Minimum horizontal swipe (px) to change slide when not zoomed */
export const IMAGE_PREVIEW_TOUCH_SWIPE_MIN_PX = 56;

/** Zoom button disabled vs min/max (floating compare) */
export const IMAGE_PREVIEW_SCALE_UI_EPSILON = 0.01;

/**
 * Default viewport wrapper for ImageUrlPreviewView (height bounds + overflow).
 * Tailwind must see full class strings (no dynamic interpolation).
 */
export const IMAGE_PREVIEW_VIEWPORT_BASE_CLASS =
    "relative min-h-[200px] max-h-[min(70vh,640px)] overflow-hidden bg-muted/20";

/** Inner flex stage min height (matches viewport floor) */
export const IMAGE_PREVIEW_STAGE_MIN_H_CLASS = "min-h-[200px]";

/** <img> max box inside the zoom/pan stage */
export const IMAGE_PREVIEW_IMG_MAX_CLASS =
    "max-h-[min(65vh,600px)] w-auto max-w-full object-contain select-none";

/** Viewport overrides: admin accounting detail dialogs (fuel / other) */
export const IMAGE_PREVIEW_VIEWPORT_ACCOUNTING_DIALOG_CLASS =
    "flex-1 min-h-[280px] max-h-[min(70vh,680px)] bg-black/5";
