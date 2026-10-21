package server

import (
	"testing"

	"github.com/stretchr/testify/require"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

func TestSetEnvironment(t *testing.T) {
	const other, team = "team", "platform"

	t.Run("sets the label and keeps the others", func(t *testing.T) {
		order := &deliveryv1alpha1.Order{}
		order.Labels = map[string]string{other: team}

		require.NoError(t, setEnvironment(order, "int"))
		require.Equal(t, map[string]string{other: team, deliveryv1alpha1.LabelEnvironment: "int"}, order.Labels)
	})

	t.Run("an empty environment removes the label", func(t *testing.T) {
		order := &deliveryv1alpha1.Order{}
		order.Labels = map[string]string{other: team, deliveryv1alpha1.LabelEnvironment: "int"}

		require.NoError(t, setEnvironment(order, ""))
		require.Equal(t, map[string]string{other: team}, order.Labels)
	})

	t.Run("an Order without labels gets them", func(t *testing.T) {
		order := &deliveryv1alpha1.Order{}

		require.NoError(t, setEnvironment(order, "prod"))
		require.Equal(t, "prod", order.Labels[deliveryv1alpha1.LabelEnvironment])
	})

	t.Run("refuses what is no label value", func(t *testing.T) {
		order := &deliveryv1alpha1.Order{}

		require.ErrorContains(t, setEnvironment(order, "int eu"), "not a valid label value")
		require.Empty(t, order.Labels)
	})
}
