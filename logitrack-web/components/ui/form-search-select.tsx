"use client";

/**
 * The react-hook-form binding of the searchable select (UI2): a `FormField` whose control is a
 * `SearchSelect` (or a `MultiSearchSelect` with `multiple`). The trigger takes the field's ref, so the
 * shared `onInvalid` handler (`lib/formInvalidHandler.ts`, ADR 0004) can focus it when the field fails, and
 * `FormControl` gives it the id, `aria-invalid` and `aria-describedby` of the form item.
 */
import type * as React from "react";
import type { Control, FieldPath, FieldValues } from "react-hook-form";

import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import {
    MultiSearchSelect,
    SearchSelect,
    type MultiSearchSelectProps,
    type SearchSelectProps,
} from "@/components/ui/search-select";

type Shared<TFieldValues extends FieldValues, TName extends FieldPath<TFieldValues>> = {
    control: Control<TFieldValues>;
    name: TName;
    label?: React.ReactNode;
    description?: React.ReactNode;
    /** Class of the FormItem (`className` styles the trigger). */
    itemClassName?: string;
};

type SingleProps = Omit<SearchSelectProps, "value" | "onValueChange" | "onBlur"> & {
    multiple?: false;
    /** Called after the field changes (a dependent field to fill, for instance). */
    onPicked?: SearchSelectProps["onValueChange"];
};
type MultiProps = Omit<MultiSearchSelectProps, "value" | "onValueChange" | "onBlur"> & {
    multiple: true;
    onPicked?: MultiSearchSelectProps["onValueChange"];
};

export type FormSearchSelectProps<TFieldValues extends FieldValues, TName extends FieldPath<TFieldValues>> = Shared<TFieldValues, TName> &
    (SingleProps | MultiProps);

export function FormSearchSelect<TFieldValues extends FieldValues, TName extends FieldPath<TFieldValues>>(
    props: FormSearchSelectProps<TFieldValues, TName>
) {
    const { control, name, label, description, itemClassName } = props;
    return (
        <FormField
            control={control}
            name={name}
            render={({ field }) => (
                <FormItem className={itemClassName}>
                    {label ? <FormLabel>{label}</FormLabel> : null}
                    <FormControl>
                        {props.multiple ? (
                            <MultiSearchSelect
                                {...stripShared(props)}
                                ref={field.ref}
                                value={Array.isArray(field.value) ? (field.value as string[]) : []}
                                onBlur={field.onBlur}
                                onValueChange={(values, options) => {
                                    field.onChange(values);
                                    props.onPicked?.(values, options);
                                }}
                            />
                        ) : (
                            <SearchSelect
                                {...stripShared(props)}
                                ref={field.ref}
                                value={(field.value as string | null | undefined) ?? ""}
                                onBlur={field.onBlur}
                                onValueChange={(value, option) => {
                                    field.onChange(value);
                                    props.onPicked?.(value, option);
                                }}
                            />
                        )}
                    </FormControl>
                    {description ? <FormDescription>{description}</FormDescription> : null}
                    <FormMessage />
                </FormItem>
            )}
        />
    );
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- drops the binding's own props before spreading the rest
function stripShared(props: any): any {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const { control, name, label, description, itemClassName, multiple, onPicked, ...rest } = props;
    return rest;
}
