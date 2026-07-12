package inject

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlways_ShouldInject(t *testing.T) {
	p := Always{}

	for i := 0; i < 10; i++ {
		assert.True(t, p.ShouldInject())
	}
}

func TestNthCall_ShouldInject(t *testing.T) {
	tests := []struct {
		name    string
		n       int
		calls   int
		expects []bool
	}{
		{
			name:    "every 2nd call",
			n:       2,
			calls:   6,
			expects: []bool{false, true, false, true, false, true},
		},
		{
			name:    "every 3rd call",
			n:       3,
			calls:   6,
			expects: []bool{false, false, true, false, false, true},
		},
		{
			name:    "every 1st call (always)",
			n:       1,
			calls:   3,
			expects: []bool{true, true, true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewNthCall(tc.n)

			for i, expected := range tc.expects {
				assert.Equal(t, expected, p.ShouldInject(), "call %d", i)
			}
		})
	}
}

func TestProbabilistic_ShouldInject(t *testing.T) {
	tests := []struct {
		name string
		prob float64
	}{
		{name: "always inject", prob: 1.0},
		{name: "never inject", prob: 0.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewProbabilistic(tc.prob)

			for i := 0; i < 100; i++ {
				result := p.ShouldInject()

				switch tc.prob {
				case 1.0:
					assert.True(t, result)
				case 0.0:
					assert.False(t, result)
				}
			}
		})
	}
}

func TestCountdown_ShouldInject(t *testing.T) {
	tests := []struct {
		name    string
		n       int
		calls   int
		expects []bool
	}{
		{
			name:    "countdown 3",
			n:       3,
			calls:   5,
			expects: []bool{true, true, true, false, false},
		},
		{
			name:    "countdown 0",
			n:       0,
			calls:   2,
			expects: []bool{false, false},
		},
		{
			name:    "countdown 1",
			n:       1,
			calls:   3,
			expects: []bool{true, false, false},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewCountdown(tc.n)

			for i, expected := range tc.expects {
				assert.Equal(t, expected, p.ShouldInject(), "call %d", i)
			}
		})
	}
}

func TestInjector_Set_And_Check(t *testing.T) {
	inj := NewInjector()
	testErr := fmt.Errorf("injected error")

	inj.Set("s3", "PutObject", testErr, Always{})

	err := inj.Check("s3", "PutObject")
	require.Error(t, err)
	assert.Equal(t, testErr, err)
}

func TestInjector_Check_NoRule(t *testing.T) {
	inj := NewInjector()

	err := inj.Check("s3", "PutObject")
	assert.NoError(t, err)
}

func TestInjector_Check_PolicySaysNo(t *testing.T) {
	inj := NewInjector()
	inj.Set("s3", "PutObject", fmt.Errorf("fail"), NewCountdown(0))

	err := inj.Check("s3", "PutObject")
	assert.NoError(t, err)
}

func TestInjector_Remove(t *testing.T) {
	inj := NewInjector()
	inj.Set("s3", "PutObject", fmt.Errorf("fail"), Always{})

	inj.Remove("s3", "PutObject")

	err := inj.Check("s3", "PutObject")
	assert.NoError(t, err)
}

func TestInjector_Reset(t *testing.T) {
	inj := NewInjector()
	inj.Set("s3", "PutObject", fmt.Errorf("fail"), Always{})
	inj.Set("ec2", "RunInstances", fmt.Errorf("fail"), Always{})

	inj.Reset()

	assert.NoError(t, inj.Check("s3", "PutObject"))
	assert.NoError(t, inj.Check("ec2", "RunInstances"))
}

func TestInjector_MultipleRules(t *testing.T) {
	inj := NewInjector()
	err1 := fmt.Errorf("s3 error")
	err2 := fmt.Errorf("ec2 error")

	inj.Set("s3", "PutObject", err1, Always{})
	inj.Set("ec2", "RunInstances", err2, Always{})

	assert.Equal(t, err1, inj.Check("s3", "PutObject"))
	assert.Equal(t, err2, inj.Check("ec2", "RunInstances"))
	assert.NoError(t, inj.Check("lambda", "Invoke"))
}
