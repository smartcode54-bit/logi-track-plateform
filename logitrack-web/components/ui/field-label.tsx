"use client";
import { createContext, useContext } from "react";

/** A Field's visible label, inherited by its control; explicit control names take precedence. */
export const FieldLabelContext = createContext<string | undefined>(undefined);
export function useFieldLabel() { return useContext(FieldLabelContext); }
