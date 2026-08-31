package glassbox

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"sync"
	"time"

	"gonum.org/v1/gonum/mat"
)

// ZCAWhitener holds the data‑dependent whitening transform:
//   x_white = W_ZCA * (x - mu)
// where  W_ZCA = U * (S + eps I)^(-1/2) * U^T
type ZCAWhitener struct {
	mu     []float64   // length D
	Wzca   *mat.Dense  // D x D matrix
	epsilon float64    // regularisation used when the matrix was built
}

// ----------  Constructor -------------------------------------------------
// data: [][]float64 where each inner slice is a 40‑dim vector (already in [0,1] after LSH)
// epsilon: small constant (e.g. 1e-6) to avoid division by zero on near‑zero eigenvalues
func ComputeZCA(data [][]float64, epsilon float64) *ZCAWhitener {
	if len(data) == 0 {
		return &ZCAWhitener{} // identity transform (will be overwritten later)
	}
	n, d := len(data), len(data[0])

	// ---- 1. pack into gonum Dense matrix ---------------------------------
	X := mat.NewDense(n, d, nil)
	for i, row := range data {
		for j, v := range row {
			X.Set(i, j, v)
		}
	}

	// ---- 2. mean ---------------------------------------------------------
	mu := make([]float64, d)
	for j := 0; j < d; j++ {
		var s float64
		for i := 0; i < n; i++ {
			s += X.At(i, j)
		}
		mu[j] = s / float64(n)
	}

	// ---- 3. centre -------------------------------------------------------
	Xc := mat.NewDense(n, d, nil)
	for i := 0; i < n; i++ {
		for j := 0; j < d; j++ {
			Xc.Set(i, j, X.At(i, j)-mu[j])
		}
	}

	// ---- 4. covariance ---------------------------------------------------
	// Sigma = (1/(n-1)) * Xc^T * Xc
	Sigma := mat.NewDense(d, d, nil)
	Sigma.Mul(Xc.T(), Xc)
	Sigma.Scale(1.0/float64(n-1), Sigma)

	// ---- 5. SVD (Sigma is symmetric PSD) -------------------------------
	var svd mat.SVD
	if ok := svd.Factorize(Sigma, mat.SVDFull); !ok {
		panic("zca: SVD failed")
	}
	var U mat.Dense
	svd.UTo(&U)
	s := svd.Values(nil) // singular values

	// ---- 6. (S + eps)^(-1/2) -------------------------------------------
	invSqrtS := mat.NewDense(d, d, nil)
	for i, val := range s {
		invSqrtS.Set(i, i, 1.0/math.Sqrt(val+epsilon))
	}

	// ---- 7. W_ZCA = U * (S+eps)^(-1/2) * U^T ---------------------------
	var tmp mat.Dense
	tmp.Mul(&U, invSqrtS)
	Wzca := mat.NewDense(d, d, nil)
	Wzca.Mul(&tmp, U.T())

	return &ZCAWhitener{
		mu:     mu,
		Wzca:   Wzca,
		epsilon: epsilon,
	}
}

// ----------  Apply -------------------------------------------------------
// Accepts a []float32 (the raw LSH vector) and returns a []float32 (whitened).
func (z *ZCAWhitener) Transform(vec []float32) []float32 {
	if z.Wzca == nil {
		// not trained yet – return as‑is
		return vec
	}
	d := len(vec)
	if d != len(z.mu) {
		// safety check – should never happen
		return vec
	}

	// v = vec - mu   (as float64 for gonum)
	v := mat.NewVecDense(d, nil)
	for i := 0; i < d; i++ {
		v.SetVec(i, float64(vec[i])-z.mu[i])
	}

	// y = Wzca * v
	var y mat.VecDense
	y.MulVec(z.Wzca, v)

	out := make([]float32, d)
	for i := 0; i < d; i++ {
		out[i] = float32(y.AtVec(i))
	}
	return out
}

// ----------  Background trainer -----------------------------------------
//
// The trainer pulls the most recent `zsamples` vectors from the `visits` table
// (where `vec` is NOT NULL) and recomputes the ZCA parameters.
//
// Call startZCATrainer() once from your application init (e.g. in main.go).
//
// Parameters you may tune:
//   - zsamples   : how many recent visits to use for the covariance estimate
//   - zcatevery  : how often to recompute (time.Duration)
//   - zcaEpsilon : regularisation for the eigen‑value inversion
//
var (
	zcaMu     sync.RWMutex // protects the global whitener
	globalZCA *ZCAWhitener // nil until first training pass finishes
)

// startZCATrainer launches a goroutine that refreshes the ZCA whitener.
func startZCATrainer(db *sql.DB, zsamples int, zcatevery time.Duration, zcaEpsilon float64) {
	if db == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(zcatevery)
		defer ticker.Stop()
		for {
			<-ticker.C
			if err := trainOnce(db, zsamples, zcaEpsilon); err != nil {
				// log but keep the old whitening (if any)
				// you can plug in your logger here
				continue
			}
		}
	}()
}

// trainOnce does a single recomputation step.
func trainOnce(db *sql.DB, zsamples int, zcaEpsilon float64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Fetch the most recent zsamples non‑null vectors.
	// The vec column is stored as a BLOB of little‑endian float32[40].
	rows, err := db.QueryContext(ctx, `
		SELECT vec FROM visits
		WHERE vec IS NOT NULL
		ORDER BY id DESC
		 LIMIT ?`,
		zsamples)
	if err != nil {
		return err
	}
	defer rows.Close()

	var data [][]float64
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return err
		}
		if len(blob) != 40*4 { // 40 float32
			continue // malformed row – skip
		}
		vec := make([]float64, 40)
		for i := 0; i < 40; i++ {
			// little‑endian float32 → float64
			bits := binary.LittleEndian.Uint32(blob[i*4:])
			vec[i] = float64(math.Float32frombits(bits))
		}
		data = append(data, vec)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(data) < 2 {
		// not enough samples yet – keep whatever we have
		return nil
	}

	// Compute new whitener
	newZCA := ComputeZCA(data, zcaEpsilon)

	// Swap in atomically
	zcaMu.Lock()
	globalZCA = newZCA
	zcaMu.Unlock()

	return nil
}

// GetZCA returns a copy of the current whitener (safe for concurrent read).
func GetZCA() *ZCAWhitener {
	zcaMu.RLock()
	defer zcaMu.RUnlock()
	if globalZCA == nil {
		return &ZCAWhitener{} // identity
	}
	// Return a shallow copy – the internal mat.Dense is safe for read‑only use.
	return &ZCAWhitener{
		mu:     append([]float64(nil), globalZCA.mu...),
		Wzca:   mat.DenseCopyOf(globalZCA.Wzca),
		epsilon: globalZCA.epsilon,
	}
}