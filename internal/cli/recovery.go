package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/recovery"
)

// Recovery bundles on the command line.
//
// A bundle is read as a third kind of input to plan and apply, and written by
// apply with --recovery-out. Neither is a separate write path: a bundle is sent
// to Alder, which validates it and returns ordinary change requests, and those
// are planned, shown, confirmed and applied exactly as --changes would be.

var recoverabilityText = map[api.RecoveryRecoverability]string{
	api.RecoverabilityExact:       "exact recovery available",
	api.RecoverabilityPartial:     "partial recovery only",
	api.RecoverabilityUnavailable: "recovery unavailable",
}

var reasonText = map[api.RecoveryReasonCode]string{
	api.RecoveryReasonPasswordNotCaptured:        "a previous password is never captured",
	api.RecoveryReasonSensitiveValueNotCaptured:  "the earlier value of a sensitive attribute is never captured",
	api.RecoveryReasonServerOwnedAttribute:       "the server maintains this attribute",
	api.RecoveryReasonIdentityRegenerated:        "a recreated entry gets a new identity and timestamps",
	api.RecoveryReasonHiddenAttributesUnknown:    "attributes this login cannot read are not restored",
	api.RecoveryReasonSensitiveValuesNotRestored: "passwords and other sensitive values are not restored",
	api.RecoveryReasonSchemaOrConfigNotSupported: "schema and configuration changes are not recovered",
	api.RecoveryReasonPreStateUnavailable:        "the entry could not be read before the change",
}

func assessmentText(level api.RecoveryRecoverability, reasons *[]api.RecoveryReason) string {
	text := recoverabilityText[level]
	if text == "" {
		text = safe(string(level))
	}
	if reasons == nil || len(*reasons) == 0 {
		return text
	}
	parts := make([]string, 0, len(*reasons))
	for _, r := range *reasons {
		part := reasonText[r.Code]
		if part == "" {
			part = safe(string(r.Code))
		}
		if r.Attribute != nil {
			part += " (" + safe(*r.Attribute) + ")"
		}
		parts = append(parts, part)
	}
	return text + ": " + strings.Join(parts, "; ")
}

// inspectRecovery sends the bundle exactly as it was read. Decoding it into the
// client's types first would drop a field this client does not know, and the
// server's refusal of an unknown field is part of treating a bundle as
// untrusted.
func inspectRecovery(ctx context.Context, r *remote, data []byte) (api.RecoveryInspection, error) {
	res, err := r.api.InspectRecoveryWithBodyWithResponse(ctx, "application/json", bytes.NewReader(data))
	if err != nil {
		return api.RecoveryInspection{}, transportFailure(ctx, "reading the recovery bundle", err)
	}
	if res.StatusCode() != http.StatusOK {
		return api.RecoveryInspection{}, r.refusal(ctx, "reading the recovery bundle", res.HTTPResponse, res.Body)
	}
	if res.JSON200 == nil {
		return api.RecoveryInspection{}, failf("unexpected_response", "Alder answered with something that is not a recovery inspection")
	}
	return *res.JSON200, nil
}

// renderRecovery describes a bundle before its plan. Every string in it came
// from a file, and goes through safe.
func renderRecovery(w io.Writer, in api.RecoveryInspection) {
	integrity := "checksum verified"
	if in.Integrity != api.SnapshotIntegrityVerified {
		integrity = "no checksum"
	}
	writef(w, "Recovery bundle created %s: %s (%s).\n", safe(in.CreatedAt), recoverabilityText[in.Recoverability], integrity)
	vendor := "an unnamed directory"
	if in.Origin.Vendor != nil {
		vendor = safe(*in.Origin.Vendor)
	}
	contexts := make([]string, 0, len(in.Origin.NamingContexts))
	for _, c := range in.Origin.NamingContexts {
		contexts = append(contexts, safe(c))
	}
	writef(w, "Made against %s (%s).\n", vendor, strings.Join(contexts, ", "))
	if !in.OriginMatches {
		var differences []string
		if in.OriginDifferences != nil {
			differences = *in.OriginDifferences
		}
		writef(w, "Warning: this directory announces a different %s. What a directory announces is not proof of which one it is.\n",
			safe(strings.Join(differences, " and ")))
	}
	for _, step := range in.Steps {
		target := ""
		if step.Original.TargetDn != nil {
			target = " -> " + safe(*step.Original.TargetDn)
		}
		writef(w, "  %d. %s %s%s: %s\n", step.Index+1, safe(step.Original.Type), safe(step.Original.Dn), target,
			assessmentText(step.Recoverability, step.Reasons))
	}
	drifted := 0
	for _, d := range in.Drift {
		if d.State == api.RecoveryDriftDrifted || d.State == api.RecoveryDriftBlocked {
			drifted++
		}
	}
	if drifted > 0 {
		writef(w, "%s no longer match the directory: the directory has drifted since the original apply.\n",
			plural(drifted, "compensating change", "compensating changes"))
	}
	writeln(w)
}

// writeRecoveryBundle writes the bundle from Alder's response, byte for byte
// as it came, indented. The file only takes its name once it has been read back
// and verified as a bundle, so a response cut short never leaves a file that
// looks like one.
func writeRecoveryBundle(path string, force bool, body []byte) error {
	var holder struct {
		Recovery json.RawMessage `json:"recovery"`
	}
	if err := json.Unmarshal(body, &holder); err != nil || len(holder.Recovery) == 0 || string(holder.Recovery) == "null" {
		return failf("recovery_missing", "Alder returned no recovery bundle")
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, holder.Recovery, "", "  "); err != nil {
		return failf("recovery_invalid", "Alder's recovery bundle is not JSON: %v", err)
	}
	pretty.WriteByte('\n')
	return writeFile(path, force, func(w io.Writer) error {
		if _, err := w.Write(pretty.Bytes()); err != nil {
			return failf("output", "cannot write %s: %v", path, err)
		}
		return nil
	}, func(f *os.File) error {
		data, err := io.ReadAll(io.LimitReader(f, int64(maxRequestBytes)+1))
		if err != nil {
			return failf("output", "cannot read back %s: %v", path, err)
		}
		if _, _, err := recovery.Decode(data); err != nil {
			return failf("recovery_invalid", "the recovery bundle Alder returned does not verify, so %s was not written: %v", path, err)
		}
		return nil
	})
}
