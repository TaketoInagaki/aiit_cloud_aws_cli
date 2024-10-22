package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/polly"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/transcribeservice"
	"github.com/aws/aws-sdk-go/service/translate"
)

func main() {
	// AWS セッション作成
	sess := session.Must(session.NewSession(&aws.Config{
		Region: aws.String("ap-northeast-1"),
	}))

	// S3バケットの指定
	bucketName := "report.3q-aws-s24745201.com"

	// 入力テキストの読み込み
	textLines, err := getInputText("./input.txt")
	if err != nil {
		fmt.Println("Error reading input file:", err)
		return
	}

	// 翻訳結果を保存するファイル
	outputFileName := "translated_text.txt"
	outputFile, err := os.Create(outputFileName)
	if err != nil {
		fmt.Println("Error creating output file:", err)
		return
	}

	defer func() {
		outputFile.Close()
		// S3アップロード後にテキストファイルを削除
		err = os.Remove(outputFileName)
		if err != nil {
			fmt.Println("Error deleting local text file:", err)
			return
		}
		fmt.Println("Deleted local text file:", outputFileName)
	}()

	writer := bufio.NewWriter(outputFile)

	for _, txt := range textLines {
		if strings.TrimSpace(txt) != "" {
			// テキストを翻訳
			translatedText, err := translateText(sess, txt)
			if err != nil {
				fmt.Println("Error translating text:", err)
				return
			}
			fmt.Println("Translated text:", translatedText)
			writer.WriteString(translatedText + "\n")

			// 翻訳結果を音声ファイルに変換し、S3にアップロード
			audioFileName, err := synthesizeSpeechAndUpload(sess, translatedText, bucketName)
			if err != nil {
				fmt.Println("Error synthesizing or uploading audio file:", err)
				return
			}

			// 音声ファイルを文字起こし
			err = transcribeAudioFile(sess, audioFileName, bucketName)
			if err != nil {
				fmt.Println("Error transcribing audio file:", err)
				return
			}
		}
	}
	writer.Flush()
}

// 翻訳対象を取得（input.txtから取得）
func getInputText(filePath string) ([]string, error) {
	inputFile, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer inputFile.Close()

	var lines []string
	scanner := bufio.NewScanner(inputFile)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// 入力テキストを英語に翻訳する（AWS SDK for Goを使用）
func translateText(sess *session.Session, text string) (string, error) {
	translateSvc := translate.New(sess)
	translateInput := &translate.TextInput{
		Text:               aws.String(text),
		SourceLanguageCode: aws.String("ja"),
		TargetLanguageCode: aws.String("en"),
	}
	translateResult, err := translateSvc.Text(translateInput)
	if err != nil {
		return "", err
	}
	return *translateResult.TranslatedText, nil
}

// 翻訳結果をもとに合成音声による音声ファイルを作成し、S3にアップロードする
func synthesizeSpeechAndUpload(sess *session.Session, text, bucketName string) (string, error) {
	pollySvc := polly.New(sess)

	// 合成音声の作成
	speechInput := &polly.SynthesizeSpeechInput{
		Text:         aws.String(text),
		OutputFormat: aws.String("mp3"),
		VoiceId:      aws.String("Joanna"),
	}
	speechOutput, err := pollySvc.SynthesizeSpeech(speechInput)
	if err != nil {
		return "", err
	}

	// 音声ファイルに保存
	audioFileName := "audioFile-" + time.Now().Format("20060102150405") + "-output.mp3"
	audioFile, err := os.Create(audioFileName)
	if err != nil {
		return "", err
	}
	defer audioFile.Close()

	_, err = audioFile.ReadFrom(speechOutput.AudioStream)
	if err != nil {
		return "", err
	}

	// 音声ファイルをS3にアップロード
	s3Svc := s3.New(sess)
	audioFile.Seek(0, 0) // 読み取り可能にするためにシーク
	_, err = s3Svc.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(audioFileName),
		Body:   audioFile,
	})
	if err != nil {
		return "", err
	}
	fmt.Println("Uploaded audio file to S3:", audioFileName)

	// S3アップロード後に音声ファイルを削除
	err = os.Remove(audioFileName)
	if err != nil {
		fmt.Println("Error deleting local audio file:", err)
		return "", err
	}
	fmt.Println("Deleted local audio file:", audioFileName)

	return audioFileName, nil
}

// 音声ファイルを文字起こしする（Transcribeを使う）
func transcribeAudioFile(sess *session.Session, audioFileName, bucketName string) error {
	transcribeSvc := transcribeservice.New(sess)

	audioFileURI := fmt.Sprintf("s3://%s/%s", bucketName, audioFileName)

	transcriptionJobName := "transcription-job-" + time.Now().Format("20060102150405")
	transcribeInput := &transcribeservice.StartTranscriptionJobInput{
		TranscriptionJobName: aws.String(transcriptionJobName),
		LanguageCode:         aws.String("en-US"),
		MediaFormat:          aws.String("mp3"),
		Media: &transcribeservice.Media{
			MediaFileUri: aws.String(audioFileURI),
		},
		OutputBucketName: aws.String(bucketName),
	}

	_, err := transcribeSvc.StartTranscriptionJob(transcribeInput)
	if err != nil {
		return err
	}

	fmt.Println("Transcription job started:", transcriptionJobName)
	return nil
}
