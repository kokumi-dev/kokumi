package service

import (
	"context"
	"fmt"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

// MenuService handles the OCI operations for a Menu.
type MenuService struct {
	oci oci.Client
}

// NewMenuService returns a new MenuService.
func NewMenuService(client oci.Client) *MenuService {
	return &MenuService{oci: client}
}

// ResolveSource resolves the Menu's consumable source: without spec.vendor the
// upstream source is resolved (digest best-effort) and advertised as-is; with
// spec.vendor the artifact is copied to the destination registry and the
// vendored ref is advertised.
func (s *MenuService) ResolveSource(ctx context.Context, menu *deliveryv1alpha1.Menu, resolver credential.PantryResolver) (*deliveryv1alpha1.MenuSourceStatus, error) {
	if menu.Spec.Vendor == nil {
		return s.resolveUpstream(ctx, menu, resolver)
	}
	return s.vendor(ctx, menu, resolver)
}

// resolveUpstream advertises the upstream source without vendoring.
func (s *MenuService) resolveUpstream(ctx context.Context, menu *deliveryv1alpha1.Menu, resolver credential.PantryResolver) (*deliveryv1alpha1.MenuSourceStatus, error) {
	resolved, client, err := resolver.ResolveSource(ctx, menu.Spec.Source, menu.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve source Pantry: %w", err)
	}

	srcRef, err := oci.Parse(resolved.OCI)
	if err != nil {
		return nil, err
	}
	srcRef.Tag = menu.Spec.Source.Version

	digest := ""
	if client != nil {
		if d, err := client.Resolve(ctx, srcRef); err != nil {
			digest = ""
		} else {
			digest = d
		}
	} else if s.oci != nil {
		if d, err := s.oci.Resolve(ctx, srcRef); err != nil {
			digest = ""
		} else {
			digest = d
		}
	}

	return &deliveryv1alpha1.MenuSourceStatus{
		OCI:       resolved.OCI,
		Version:   menu.Spec.Source.Version,
		PantryRef: menu.Spec.Source.PantryRef,
		Digest:    digest,
	}, nil
}

// vendor copies the source artifact to the destination registry and advertises
// the vendored ref.
func (s *MenuService) vendor(ctx context.Context, menu *deliveryv1alpha1.Menu, resolver credential.PantryResolver) (*deliveryv1alpha1.MenuSourceStatus, error) {
	resolved, srcClient, err := resolver.ResolveSource(ctx, menu.Spec.Source, menu.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve source Pantry: %w", err)
	}

	destURL, destClient, err := s.resolveDestination(ctx, menu, resolver)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve vendor destination: %w", err)
	}

	srcRef, err := oci.Parse(resolved.OCI)
	if err != nil {
		return nil, err
	}
	srcRef.Tag = menu.Spec.Source.Version

	dstRef, err := oci.Parse(destURL)
	if err != nil {
		return nil, err
	}
	dstRef.Tag = menu.Spec.Source.Version

	source := s.oci
	if source == nil {
		source = srcClient
	}
	if source == nil {
		source = oci.NewORASClient()
	}

	if err := source.Copy(ctx, destClient, srcRef, dstRef); err != nil {
		return nil, fmt.Errorf("failed to vendor source: %w", err)
	}

	digest, err := destClient.Resolve(ctx, dstRef)
	if err != nil {
		digest, err = source.Resolve(ctx, dstRef)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve vendored digest: %w", err)
		}
	}

	return &deliveryv1alpha1.MenuSourceStatus{
		OCI:       destURL,
		Version:   menu.Spec.Source.Version,
		PantryRef: menu.Spec.Vendor.Destination.PantryRef,
		Digest:    digest,
	}, nil
}

// resolveDestination resolves a VendorDestination into a URL and client.
func (s *MenuService) resolveDestination(ctx context.Context, menu *deliveryv1alpha1.Menu, resolver credential.PantryResolver) (string, oci.Client, error) {
	if menu.Spec.Vendor.Destination.PantryRef != nil {
		resolved, c, err := resolver.ResolveSource(ctx, deliveryv1alpha1.OCISource{
			PantryRef: menu.Spec.Vendor.Destination.PantryRef,
		}, menu.Namespace)
		if err != nil {
			return "", nil, err
		}
		return resolved.OCI, c, nil
	}

	return menu.Spec.Vendor.Destination.OCI, oci.NewORASClient(), nil
}
