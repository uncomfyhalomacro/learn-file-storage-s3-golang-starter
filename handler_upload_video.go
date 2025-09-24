package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func getVideoExtension(s string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(s)
	if err != nil {
		return "", err
	}
	items := strings.Split(mediaType, "/")
	if len(items) < 2 || len(items) > 2 {
		return "", fmt.Errorf("not a valid video mime-type")
	}
	kind, extension := items[0], items[1]
	if kind == "video" {
		return extension, nil
	}
	return "", fmt.Errorf("not an video")
}

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}

	user, err := cfg.db.GetUser(userID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "User not found", err)
		return
	}

	if video.UserID != user.ID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 30
	http.MaxBytesReader(w, r.Body, maxMemory)

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error processing request", err)
		return

	}
	defer file.Close()
	contentType := header.Header.Get("Content-Type") // just assume that even an empty mime-type is valid
	extension, err := getVideoExtension(contentType)

	if err != nil {
		respondWithError(w, http.StatusNotAcceptable, "Not acceptable", err)
		return

	}

	videoFilename := fmt.Sprintf("%s.%s", video.ID.String(), extension)
	videoFile, err := os.CreateTemp("", videoFilename)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Server error", err)
		return
	}
	defer videoFile.Close()
	defer os.Remove(videoFile.Name())

	_, err = io.Copy(videoFile, file)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Server error", err)
		return
	}

	_, err = videoFile.Seek(0, 0)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Server error", err)
		return
	}

	numBytes := 32
	randomBytes := make([]byte, numBytes)

	// Read random bytes into the slice
	_, err = rand.Read(randomBytes)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error generating random bytes", err)
		return
	}

	// Encode the random bytes into a hexadecimal string
	hexString := hex.EncodeToString(randomBytes)

	keyFilename := fmt.Sprintf("%s.%s", hexString, extension)

	// upload to S3
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         aws.String(keyFilename),
		Body:        videoFile,
		ContentType: &contentType,
	})

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error uploading to S3", err)
		return
	}

	scheme := "https"

	videoUrl := fmt.Sprintf("%s://%s.s3.%s.amazonaws.com/%s", scheme, cfg.s3Bucket, cfg.s3Region, keyFilename)
	video.UpdatedAt = time.Now()
	video.VideoURL = &videoUrl

	err = cfg.db.UpdateVideo(video)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating data", err)
		return

	}

	respondWithJSON(w, http.StatusOK, video)
}
