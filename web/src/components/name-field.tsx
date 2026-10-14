import { useState } from "react";

import { Input } from "@/components/ui/input";
import { AppLookPicker } from "@/components/app-look-picker";
import { FormField } from "@/components/form-field";
import { suggestAppLook } from "@/lib/app-look";
import { invalidAttrs } from "@/lib/field-errors";

type Value = { name: string; icon: string; color: string };

// NameField is a new app's name with its tile beside it. While the user
// hasn't picked an icon or colour, the name picks them (suggestAppLook);
// a pick sticks until "Pick automatically from the name".
export function NameField({
  value,
  onChange,
  error,
  id = "app-name",
}: {
  value: Value;
  onChange: (next: Value) => void;
  error?: string;
  id?: string;
}) {
  const [picked, setPicked] = useState(false);
  return (
    <FormField id={id} label="Name" error={error}>
      <div className="flex items-center gap-2">
        <AppLookPicker
          name={value.name}
          icon={value.icon}
          color={value.color}
          onPick={(look) => {
            setPicked(true);
            onChange({ ...value, ...look });
          }}
          onReset={() => {
            setPicked(false);
            onChange({ ...value, ...suggestAppLook(value.name) });
          }}
        />
        <Input
          id={id}
          value={value.name}
          placeholder="my-app"
          autoComplete="off"
          {...invalidAttrs(id, error)}
          onChange={(e) => {
            const name = e.target.value;
            onChange(picked ? { ...value, name } : { name, ...suggestAppLook(name) });
          }}
        />
      </div>
    </FormField>
  );
}
