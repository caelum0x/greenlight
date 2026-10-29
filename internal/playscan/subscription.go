package playscan

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// subscriptionSourceExts are the Android app-source files worth reading for
// subscription signals. Kotlin and Java carry the billing call sites; XML
// (string resources and Compose-adjacent layout files some projects still
// ship) is where the required disclosure copy usually lives when it is not
// hardcoded directly in a Composable or Activity.
var subscriptionSourceExts = map[string]bool{
	".kt": true, ".kts": true, ".java": true, ".xml": true,
}

// reSubscriptionAPI matches subscription-specific Play Billing and RevenueCat
// call sites. This is deliberately narrower than "ships Play Billing at all":
// com.android.billingclient:billing is also the dependency for pure one-time
// IAP, so a Gradle-only signal would false-positive on every app that never
// sells a subscription. ProductType.SUBS / SkuType.SUBS / querySubscriptions /
// SubscriptionOfferDetails only appear in code that actually queries or
// builds a subscription purchase flow.
var reSubscriptionAPI = regexp.MustCompile(`(?i)(BillingClient\.ProductType\.SUBS|ProductType\.SUBS|BillingClient\.SkuType\.SUBS|SkuType\.SUBS|SubscriptionOfferDetails|querySubscriptions\s*\(|purchasePackage|PurchasesPackage)`)

// reRenewalDisclosure matches the required Play Subscriptions policy wording
// ("automatic renewal terms"): the subscription renews unless the user
// cancels. Same wording shape as codescan's iOS §3.1.2 anti-pattern, since the
// disclosure requirement is the same user-facing sentence on either platform.
var reRenewalDisclosure = regexp.MustCompile(`(?i)(auto-?renew|automatically renew|renews automatically|renewed automatically)`)

// subscriptionSignals is what the policy rule needs out of the app's Android
// source — both findable only by actually reading .kt/.java/.xml content,
// which nothing else in this package does (manifest and Gradle parsing only).
type subscriptionSignals struct {
	HasSubscriptionAPI bool
	File               string
	Line               int
	HasDisclosure      bool
}

// scanSubscriptionSource walks the Android source tree once, looking for a
// subscription-specific billing call site and, independently, the renewal
// disclosure wording anywhere in the project. A project with neither Kotlin,
// Java, nor XML files (an empty or non-Android scan root) comes back zero
// value, which callers treat as "nothing to report" rather than an error.
func scanSubscriptionSource(root string) subscriptionSignals {
	var sig subscriptionSignals
	errStop := errors.New("found both signals")

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !subscriptionSourceExts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		// The manifest is already parsed elsewhere for its own purpose and
		// never contains billing call sites or disclosure prose, so reading
		// it again here would only cost time.
		if d.Name() == "AndroidManifest.xml" {
			return nil
		}

		lines, rerr := readSourceLines(path)
		if rerr != nil {
			return nil
		}
		relPath, _ := filepath.Rel(root, path)
		for i, line := range lines {
			if !sig.HasSubscriptionAPI && reSubscriptionAPI.MatchString(line) {
				sig.HasSubscriptionAPI = true
				sig.File = filepath.ToSlash(relPath)
				sig.Line = i + 1
			}
			if !sig.HasDisclosure && reRenewalDisclosure.MatchString(line) {
				sig.HasDisclosure = true
			}
			if sig.HasSubscriptionAPI && sig.HasDisclosure {
				return errStop
			}
		}
		return nil
	})

	return sig
}

func readSourceLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}
