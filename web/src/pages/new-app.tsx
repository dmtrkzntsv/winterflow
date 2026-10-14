import { useSearchParams } from "react-router-dom";

import CreateAppPage from "@/pages/create-app";
import CreateFromTemplatePage from "@/pages/create-from-template";

// NewAppPage is /server/:id/new: ?from=template opens the template form,
// anything else the compose editor (which also deploys from Git). Phase 3
// adds the chooser and the per-source forms.
export default function NewAppPage() {
  const [params] = useSearchParams();
  return params.get("from") === "template" ? <CreateFromTemplatePage /> : <CreateAppPage />;
}
