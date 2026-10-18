package registry

import (
	"cmp"
	"maps"
	"slices"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// OrderLocations are the registry locations an Order renders from and to,
// with the connections Kokumi uses for them.
type OrderLocations struct {
	// Product is the Scope of the Product's Registries, which connect to the
	// source, the images and the location the images are vendored to.
	Product    Scope
	Source     string
	SourceConn Connection
	// Destination is where the Order's Preparations are pushed, through the
	// Order's Registries.
	Destination     string
	DestinationConn Connection
	// ImagesVendorURL is the location the Product vendors images below, ""
	// when it does not.
	ImagesVendorURL string
	// Images are the images of the Order's latest Preparation.
	Images []deliveryv1alpha1.PreparationImage
}

// OrderAccess returns the registry locations of an Order, sorted: the
// source and the images Kokumi pulls, through the Product's Registries; the
// destination it pushes to, through the Order's; the location the images
// are vendored to; and what Argo CD and the kubelet pull, for which they need
// credentials of their own.
func OrderAccess(l OrderLocations) []deliveryv1alpha1.RegistryAccess {
	registries := l.Product.Registries()
	registryOf := func(url string) string {
		reg, err := Match(registries, url)
		if err != nil || reg == nil {
			return ""
		}
		return reg.Name
	}

	accessByKey := map[deliveryv1alpha1.RegistryAccess]bool{}
	add := func(url string, op deliveryv1alpha1.RegistryOperation, who deliveryv1alpha1.RegistryClient, registryName string) {
		accessByKey[deliveryv1alpha1.RegistryAccess{URL: url, Operation: op, Client: who, Registry: registryName}] = true
	}

	add(l.Source, deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientKokumi, l.SourceConn.RegistryName())
	add(l.Destination, deliveryv1alpha1.RegistryOperationPush, deliveryv1alpha1.RegistryClientKokumi, l.DestinationConn.RegistryName())
	add(l.Destination, deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientArgoCD, "")

	vendorImages := ""
	if l.ImagesVendorURL != "" {
		vendorImages = l.ImagesVendorURL + "/images"
	}
	for _, img := range l.Images {
		loc, err := ParseLocation(img.Name)
		if err != nil {
			continue
		}
		upstream := "oci://" + loc.Host
		if reg := registryOf(img.Name); reg != "" {
			upstream = urlOf(registries, reg)
		}
		add(upstream, deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientKokumi, registryOf(img.Name))

		if img.Vendored != "" && vendorImages != "" {
			add(vendorImages, deliveryv1alpha1.RegistryOperationPush, deliveryv1alpha1.RegistryClientKokumi, registryOf(vendorImages))
			add(vendorImages, deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientKubelet, "")
			continue
		}
		add("oci://"+loc.Host, deliveryv1alpha1.RegistryOperationPull, deliveryv1alpha1.RegistryClientKubelet, "")
	}

	return slices.SortedFunc(maps.Keys(accessByKey), func(a, b deliveryv1alpha1.RegistryAccess) int {
		return cmp.Or(cmp.Compare(a.URL, b.URL), cmp.Compare(a.Client, b.Client), cmp.Compare(a.Operation, b.Operation))
	})
}

// ProductAccess returns the registry locations of a Product: its source,
// and its vendor location when vendor is set.
func ProductAccess(source string, sourceConn Connection, vendor string, vendorConn Connection) []deliveryv1alpha1.RegistryAccess {
	access := []deliveryv1alpha1.RegistryAccess{{
		URL: source, Operation: deliveryv1alpha1.RegistryOperationPull, Client: deliveryv1alpha1.RegistryClientKokumi, Registry: sourceConn.RegistryName(),
	}}
	if vendor != "" {
		access = append(access, deliveryv1alpha1.RegistryAccess{
			URL: vendor, Operation: deliveryv1alpha1.RegistryOperationPush, Client: deliveryv1alpha1.RegistryClientKokumi, Registry: vendorConn.RegistryName(),
		})
	}
	return access
}

// urlOf returns the url of the named Registry among registries.
func urlOf(registries []deliveryv1alpha1.Registry, name string) string {
	for _, reg := range registries {
		if reg.Name == name {
			return reg.Spec.URL
		}
	}
	return ""
}
