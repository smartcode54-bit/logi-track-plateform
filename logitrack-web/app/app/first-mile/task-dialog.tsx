"use client";

import { FirstMileTaskDialog as FeatureDialog } from "@/features/tasks";
import { Task as FirstMileTask } from "@/validate/taskSchema";

interface ItemDialogProps {
    mode: "create" | "edit";
    task?: Partial<FirstMileTask>;
    trigger?: React.ReactNode;
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    onSuccess?: () => void;
    taskType?: "FIRST_MILE" | "LINE_HAUL";
    onTaskTypeChange?: (taskType: "FIRST_MILE" | "LINE_HAUL") => void;
    embedded?: boolean;
}

export function FirstMileTaskDialog(props: ItemDialogProps) {
    return <FeatureDialog {...props} />;
}

